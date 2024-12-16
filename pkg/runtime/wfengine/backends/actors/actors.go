/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package actors

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets/workflow"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.wfengine.backend.actors")

const (
	defaultNamespace     = "default"
	WorkflowNameLabelKey = "workflow"
	ActivityNameLabelKey = "activity"
	ActorTypePrefix      = "dapr.internal."
)

type Options struct {
	AppID              string
	Namespace          string
	Actors             actors.Interface
	Resiliency         resiliency.Provider
	SchedulerReminders bool
}

type Actors struct {
	appID             string
	workflowActorType string
	activityActorType string

	// TODO: @joshvanl
	cachingDisabled         bool
	defaultWorkflowTimeout  *time.Duration
	defaultActivityTimeout  *time.Duration
	defaultReminderInterval *time.Duration
	resiliency              resiliency.Provider
	actors                  actors.Interface
	schedulerReminders      bool

	// TODO: @joshvanl: remove
	orchestrationWorkItemChan chan *backend.OrchestrationWorkItem
	activityWorkItemChan      chan *backend.ActivityWorkItem
}

func New(opts Options) (*Actors, error) {
	return &Actors{
		appID:              opts.AppID,
		workflowActorType:  ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + WorkflowNameLabelKey,
		activityActorType:  ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + ActivityNameLabelKey,
		actors:             opts.Actors,
		resiliency:         opts.Resiliency,
		schedulerReminders: opts.SchedulerReminders,

		// TODO: @joshvanl: remove
		orchestrationWorkItemChan: make(chan *backend.OrchestrationWorkItem),
		activityWorkItemChan:      make(chan *backend.ActivityWorkItem),
	}, nil
}

// getWorkflowScheduler returns a WorkflowScheduler func that sends an orchestration work item to the Durable Task Framework.
// TODO: @joshvanl: remove
func getWorkflowScheduler(orchestrationWorkItemChan chan *backend.OrchestrationWorkItem) todo.WorkflowScheduler {
	return func(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
		log.Debugf("%s: scheduling workflow execution with durabletask engine", wi.InstanceID)
		select {
		case <-ctx.Done(): // <-- engine is shutting down or a caller timeout expired
			return ctx.Err()
		case orchestrationWorkItemChan <- wi: // blocks until the engine is ready to process the work item
			return nil
		}
	}
}

// getActivityScheduler returns an activityScheduler func that sends an activity work item to the Durable Task Framework.
// TODO: @joshvanl: remove
func getActivityScheduler(activityWorkItemChan chan *backend.ActivityWorkItem) todo.ActivityScheduler {
	return func(ctx context.Context, wi *backend.ActivityWorkItem) error {
		log.Debugf(
			"%s: scheduling [%s#%d] activity execution with durabletask engine",
			wi.InstanceID,
			wi.NewEvent.GetTaskScheduled().GetName(),
			wi.NewEvent.GetEventId())
		select {
		case <-ctx.Done(): // engine is shutting down
			return ctx.Err()
		case activityWorkItemChan <- wi: // blocks until the engine is ready to process the work item
			return nil
		}
	}
}

func (abe *Actors) RegisterActors(ctx context.Context) error {
	atable, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	atable.RegisterActorTypes(
		table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{
				{
					Type: abe.workflowActorType,
					Factory: workflow.WorkflowFactory(workflow.WorkflowOptions{
						AppID:              abe.appID,
						WorkflowActorType:  abe.workflowActorType,
						ActivityActorType:  abe.activityActorType,
						CachingDisabled:    abe.cachingDisabled,
						DefaultTimeout:     abe.defaultWorkflowTimeout,
						ReminderInterval:   abe.defaultReminderInterval,
						Resiliency:         abe.resiliency,
						Actors:             abe.actors,
						Scheduler:          getWorkflowScheduler(abe.orchestrationWorkItemChan),
						SchedulerReminders: abe.schedulerReminders,
					}),
				},
				{
					Type: abe.activityActorType,
					Factory: workflow.ActivityFactory(workflow.ActivityOptions{
						AppID:              abe.appID,
						ActivityActorType:  abe.activityActorType,
						WorkflowActorType:  abe.workflowActorType,
						CachingDisabled:    abe.cachingDisabled,
						DefaultTimeout:     abe.defaultActivityTimeout,
						ReminderInterval:   abe.defaultReminderInterval,
						Scheduler:          getActivityScheduler(abe.activityWorkItemChan),
						Actors:             abe.actors,
						SchedulerReminders: abe.schedulerReminders,
					}),
				},
			},
		},
	)

	return nil
}

func (abe *Actors) UnRegisterActors(ctx context.Context) error {
	table, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	return table.UnRegisterActorTypes(abe.workflowActorType, abe.activityActorType)
}

// CreateOrchestrationInstance implements backend.Backend and creates a new workflow instance.
//
// Internally, creating a workflow instance also creates a new actor with the same ID. The create
// request is saved into the actor's "inbox" and then executed via a reminder thread. If the app is
// scaled out across multiple replicas, the actor might get assigned to a replicas other than this one.
func (abe *Actors) CreateOrchestrationInstance(ctx context.Context, e *backend.HistoryEvent, opts ...backend.OrchestrationIdReusePolicyOptions) error {
	var workflowInstanceID string
	if es := e.GetExecutionStarted(); es == nil {
		return errors.New("the history event must be an ExecutionStartedEvent")
	} else if oi := es.GetOrchestrationInstance(); oi == nil {
		return errors.New("the ExecutionStartedEvent did not contain orchestration instance information")
	} else {
		workflowInstanceID = oi.GetInstanceId()
	}

	policy := &api.OrchestrationIdReusePolicy{}
	for _, opt := range opts {
		opt(policy)
	}

	eventData, err := backend.MarshalHistoryEvent(e)
	if err != nil {
		return err
	}

	requestBytes, err := json.Marshal(todo.CreateWorkflowInstanceRequest{
		Policy:          policy,
		StartEventBytes: eventData,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal CreateWorkflowInstanceRequest: %w", err)
	}

	// Invoke the well-known workflow actor directly, which will be created by this invocation request.
	// Note that this request goes directly to the actor runtime, bypassing the API layer.
	req := internalsv1pb.NewInternalInvokeRequest(todo.CreateWorkflowInstanceMethod).
		WithActor(abe.workflowActorType, workflowInstanceID).
		WithData(requestBytes).
		WithContentType(invokev1.JSONContentType)
	start := time.Now()

	engine, err := abe.actors.Engine(ctx)
	if err != nil {
		return err
	}

	_, err = engine.Call(ctx, req)
	elapsed := diag.ElapsedSince(start)
	if err != nil {
		// failed request to CREATE workflow, record count and latency metrics.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.CreateWorkflow, diag.StatusFailed, elapsed)
		return err
	}
	// successful request to CREATE workflow, record count and latency metrics.
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.CreateWorkflow, diag.StatusSuccess, elapsed)
	return nil
}

// GetOrchestrationMetadata implements backend.Backend
func (abe *Actors) GetOrchestrationMetadata(ctx context.Context, id api.InstanceID) (*api.OrchestrationMetadata, error) {
	// Invoke the corresponding actor, which internally stores its own workflow metadata
	req := internalsv1pb.
		NewInternalInvokeRequest(todo.GetWorkflowMetadataMethod).
		WithActor(abe.workflowActorType, string(id)).
		WithContentType(invokev1.OctetStreamContentType)

	engine, err := abe.actors.Engine(ctx)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	res, err := engine.Call(ctx, req)
	elapsed := diag.ElapsedSince(start)
	if err != nil {
		// failed request to GET workflow Information, record count and latency metrics.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.GetWorkflow, diag.StatusFailed, elapsed)
		return nil, err
	}
	// successful request to GET workflow information, record count and latency metrics.
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.GetWorkflow, diag.StatusSuccess, elapsed)

	var metadata api.OrchestrationMetadata
	if err := gob.NewDecoder(bytes.NewReader(res.GetMessage().GetData().GetValue())).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to decode the internal actor response: %w", err)
	}
	return &metadata, nil
}

// AbandonActivityWorkItem implements backend.Backend. It gets called by durabletask-go when there is
// an unexpected failure in the workflow activity execution pipeline.
func (*Actors) AbandonActivityWorkItem(ctx context.Context, wi *backend.ActivityWorkItem) error {
	log.Warnf("%s: aborting activity execution (::%d)", wi.InstanceID, wi.NewEvent.GetEventId())

	// Sending false signals the waiting activity actor to abort the activity execution.
	if channel, ok := wi.Properties[todo.CallbackChannelProperty]; ok {
		channel.(chan bool) <- false
	}
	return nil
}

// AbandonOrchestrationWorkItem implements backend.Backend. It gets called by durabletask-go when there is
// an unexpected failure in the workflow orchestration execution pipeline.
func (*Actors) AbandonOrchestrationWorkItem(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
	log.Warnf("%s: aborting workflow execution", wi.InstanceID)

	// Sending false signals the waiting workflow actor to abort the workflow execution.
	if channel, ok := wi.Properties[todo.CallbackChannelProperty]; ok {
		channel.(chan bool) <- false
	}
	return nil
}

// AddNewOrchestrationEvent implements backend.Backend and sends the event e to the workflow actor identified by id.
func (abe *Actors) AddNewOrchestrationEvent(ctx context.Context, id api.InstanceID, e *backend.HistoryEvent) error {
	data, err := backend.MarshalHistoryEvent(e)
	if err != nil {
		return err
	}

	// Send the event to the corresponding workflow actor, which will store it in its event inbox.
	req := internalsv1pb.
		NewInternalInvokeRequest(todo.AddWorkflowEventMethod).
		WithActor(abe.workflowActorType, string(id)).
		WithData(data).
		WithContentType(invokev1.OctetStreamContentType)

	engine, err := abe.actors.Engine(ctx)
	if err != nil {
		return err
	}

	start := time.Now()
	_, err = engine.Call(ctx, req)
	elapsed := diag.ElapsedSince(start)
	if err != nil {
		// failed request to ADD EVENT, record count and latency metrics.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.AddEvent, diag.StatusFailed, elapsed)
		return err
	}
	// successful request to ADD EVENT, record count and latency metrics.
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.AddEvent, diag.StatusSuccess, elapsed)
	return nil
}

// CompleteActivityWorkItem implements backend.Backend
func (*Actors) CompleteActivityWorkItem(ctx context.Context, wi *backend.ActivityWorkItem) error {
	// Sending true signals the waiting activity actor to complete the execution normally.
	wi.Properties[todo.CallbackChannelProperty].(chan bool) <- true
	return nil
}

// CompleteOrchestrationWorkItem implements backend.Backend
func (*Actors) CompleteOrchestrationWorkItem(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
	// Sending true signals the waiting workflow actor to complete the execution normally.
	wi.Properties[todo.CallbackChannelProperty].(chan bool) <- true
	return nil
}

// CreateTaskHub implements backend.Backend
func (*Actors) CreateTaskHub(context.Context) error {
	return nil
}

// DeleteTaskHub implements backend.Backend
func (*Actors) DeleteTaskHub(context.Context) error {
	return errors.New("not supported")
}

// GetActivityWorkItem implements backend.Backend
func (abe *Actors) GetActivityWorkItem(ctx context.Context) (*backend.ActivityWorkItem, error) {
	// Wait for the activity actor to signal us with some work to do
	log.Debug("Actor backend is waiting for an activity actor to schedule an invocation.")
	select {
	case wi := <-abe.activityWorkItemChan:
		log.Debugf(
			"Actor backend received a [%s#%d] activity task for workflow '%s'.",
			wi.NewEvent.GetTaskScheduled().GetName(),
			wi.NewEvent.GetEventId(),
			wi.InstanceID)
		return wi, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetOrchestrationRuntimeState implements backend.Backend
func (abe *Actors) GetOrchestrationRuntimeState(ctx context.Context, owi *backend.OrchestrationWorkItem) (*backend.OrchestrationRuntimeState, error) {
	// Invoke the corresponding actor, which internally stores its own workflow state.
	req := internalsv1pb.
		NewInternalInvokeRequest(todo.GetWorkflowStateMethod).
		WithActor(abe.workflowActorType, string(owi.InstanceID)).
		WithContentType(invokev1.OctetStreamContentType)

	engine, err := abe.actors.Engine(ctx)
	if err != nil {
		return nil, err
	}

	res, err := engine.Call(ctx, req)
	if err != nil {
		return nil, err
	}
	wfState := &state.State{}
	err = wfState.DecodeWorkflowState(res.GetMessage().GetData().GetValue())
	if err != nil {
		return nil, fmt.Errorf("failed to decode the internal actor response: %w", err)
	}
	// TODO: Add caching when a good invalidation policy can be determined
	runtimeState := backend.NewOrchestrationRuntimeState(owi.InstanceID, wfState.History)

	return runtimeState, nil
}

// GetOrchestrationWorkItem implements backend.Backend
func (abe *Actors) GetOrchestrationWorkItem(ctx context.Context) (*backend.OrchestrationWorkItem, error) {
	// Wait for the workflow actor to signal us with some work to do
	log.Debug("Actor backend is waiting for a workflow actor to schedule an invocation.")
	select {
	case wi := <-abe.orchestrationWorkItemChan:
		log.Debugf("Actor backend received a workflow task for workflow '%s'.", wi.InstanceID)
		return wi, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PurgeOrchestrationState deletes all saved state for the specific orchestration instance.
func (abe *Actors) PurgeOrchestrationState(ctx context.Context, id api.InstanceID) error {
	req := internalsv1pb.
		NewInternalInvokeRequest(todo.PurgeWorkflowStateMethod).
		WithActor(abe.workflowActorType, string(id))

	engine, err := abe.actors.Engine(ctx)
	if err != nil {
		return err
	}

	start := time.Now()
	_, err = engine.Call(ctx, req)
	elapsed := diag.ElapsedSince(start)
	if err != nil {
		// failed request to PURGE WORKFLOW, record latency and count metrics.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.PurgeWorkflow, diag.StatusFailed, elapsed)
		return err
	}
	// successful request to PURGE WORKFLOW, record latency and count metrics.
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.PurgeWorkflow, diag.StatusSuccess, elapsed)
	return nil
}

// Start implements backend.Backend
func (abe *Actors) Start(ctx context.Context) error {
	return nil
}

// Stop implements backend.Backend
func (*Actors) Stop(context.Context) error {
	return nil
}

// String displays the type information
func (abe *Actors) String() string {
	return "dapr.actors/v1-beta"
}
