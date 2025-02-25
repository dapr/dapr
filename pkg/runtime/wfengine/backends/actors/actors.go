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
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/cenkalti/backoff/v4"

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
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/concurrency"
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
	EventSink          workflow.EventSink
}

type Actors struct {
	appID             string
	workflowActorType string
	activityActorType string

	defaultReminderInterval *time.Duration
	resiliency              resiliency.Provider
	actors                  actors.Interface
	schedulerReminders      bool
	eventSink               workflow.EventSink

	orchestrationWorkItemChan chan *backend.OrchestrationWorkItem
	activityWorkItemChan      chan *backend.ActivityWorkItem

	registeredCh chan struct{}
	lock         sync.RWMutex
}

func New(opts Options) *Actors {
	return &Actors{
		appID:                     opts.AppID,
		workflowActorType:         ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + WorkflowNameLabelKey,
		activityActorType:         ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + ActivityNameLabelKey,
		actors:                    opts.Actors,
		resiliency:                opts.Resiliency,
		schedulerReminders:        opts.SchedulerReminders,
		orchestrationWorkItemChan: make(chan *backend.OrchestrationWorkItem, 1),
		activityWorkItemChan:      make(chan *backend.ActivityWorkItem, 1),
		registeredCh:              make(chan struct{}),
		eventSink:                 opts.EventSink,
	}
}

func (abe *Actors) RegisterActors(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()

	atable, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	workflowFactory, err := workflow.WorkflowFactory(ctx, workflow.WorkflowOptions{
		AppID:             abe.appID,
		WorkflowActorType: abe.workflowActorType,
		ActivityActorType: abe.activityActorType,
		ReminderInterval:  abe.defaultReminderInterval,
		Resiliency:        abe.resiliency,
		Actors:            abe.actors,
		Scheduler: func(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
			log.Debugf("%s: scheduling workflow execution with durabletask engine", wi.InstanceID)
			select {
			case <-ctx.Done(): // <-- engine is shutting down or a caller timeout expired
				return ctx.Err()
			case abe.orchestrationWorkItemChan <- wi: // blocks until the engine is ready to process the work item
				return nil
			}
		},
		SchedulerReminders: abe.schedulerReminders,
		EventSink:          abe.eventSink,
	})
	if err != nil {
		return err
	}

	activityFactory, err := workflow.ActivityFactory(ctx, workflow.ActivityOptions{
		AppID:             abe.appID,
		ActivityActorType: abe.activityActorType,
		WorkflowActorType: abe.workflowActorType,
		ReminderInterval:  abe.defaultReminderInterval,
		Scheduler: func(ctx context.Context, wi *backend.ActivityWorkItem) error {
			log.Debugf(
				"%s: scheduling [%s#%d] activity execution with durabletask engine",
				wi.InstanceID,
				wi.NewEvent.GetTaskScheduled().GetName(),
				wi.NewEvent.GetEventId())
			select {
			case <-ctx.Done(): // engine is shutting down
				return ctx.Err()
			case abe.activityWorkItemChan <- wi: // blocks until the engine is ready to process the work item
				return nil
			}
		},
		Actors:             abe.actors,
		SchedulerReminders: abe.schedulerReminders,
	})
	if err != nil {
		return err
	}

	atable.RegisterActorTypes(
		table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{
				{
					Factory: workflowFactory,
					Type:    abe.workflowActorType,
				},
				{
					Factory: activityFactory,
					Type:    abe.activityActorType,
				},
			},
		},
	)

	close(abe.registeredCh)

	return nil
}

func (abe *Actors) UnRegisterActors(ctx context.Context) error {
	table, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	defer func() {
		abe.lock.Lock()
		abe.registeredCh = make(chan struct{})
		abe.lock.Unlock()
	}()

	return table.UnRegisterActorTypes(abe.workflowActorType, abe.activityActorType)
}

// CreateOrchestrationInstance implements backend.Backend and creates a new workflow instance.
//
// Internally, creating a workflow instance also creates a new actor with the same ID. The create
// request is saved into the actor's "inbox" and then executed via a reminder thread. If the app is
// scaled out across multiple replicas, the actor might get assigned to a replicas other than this one.
func (abe *Actors) CreateOrchestrationInstance(ctx context.Context, e *backend.HistoryEvent, opts ...backend.OrchestrationIdReusePolicyOptions) error {
	abe.lock.RLock()
	ch := abe.registeredCh
	abe.lock.RUnlock()

	select {
	case <-ch:
	case <-ctx.Done():
		return ctx.Err()
	}

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

	requestBytes, err := proto.Marshal(&backend.CreateWorkflowInstanceRequest{
		Policy:     policy,
		StartEvent: e,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal CreateWorkflowInstanceRequest: %w", err)
	}

	// Invoke the well-known workflow actor directly, which will be created by this invocation request.
	// Note that this request goes directly to the actor runtime, bypassing the API layer.
	req := internalsv1pb.NewInternalInvokeRequest(todo.CreateWorkflowInstanceMethod).
		WithActor(abe.workflowActorType, workflowInstanceID).
		WithData(requestBytes).
		WithContentType(invokev1.ProtobufContentType)
	start := time.Now()

	engine, err := abe.actors.Engine(ctx)
	if err != nil {
		return err
	}

	err = backoff.Retry(func() error {
		_, eerr := engine.Call(ctx, req)
		status, ok := status.FromError(eerr)
		if ok && status.Code() == codes.FailedPrecondition {
			return eerr
		}
		return backoff.Permanent(eerr)
	}, backoff.WithContext(backoff.NewConstantBackOff(time.Second), ctx))

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
func (abe *Actors) GetOrchestrationMetadata(ctx context.Context, id api.InstanceID) (*backend.OrchestrationMetadata, error) {
	state, err := abe.loadInternalState(ctx, id)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, api.ErrInstanceNotFound
	}

	rstate := runtimestate.NewOrchestrationRuntimeState(string(id), state.CustomStatus, state.History)

	name, _ := runtimestate.Name(rstate)
	createdAt, _ := runtimestate.CreatedTime(rstate)
	lastUpdated, _ := runtimestate.LastUpdatedTime(rstate)
	input, _ := runtimestate.Input(rstate)
	output, _ := runtimestate.Output(rstate)
	failureDetuils, _ := runtimestate.FailureDetails(rstate)

	return &backend.OrchestrationMetadata{
		InstanceId:     string(id),
		Name:           name,
		RuntimeStatus:  runtimestate.RuntimeStatus(rstate),
		CreatedAt:      timestamppb.New(createdAt),
		LastUpdatedAt:  timestamppb.New(lastUpdated),
		Input:          input,
		Output:         output,
		CustomStatus:   rstate.GetCustomStatus(),
		FailureDetails: failureDetuils,
	}, nil
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
	// TODO: @joshvanl: remove
	if channel, ok := wi.Properties[todo.CallbackChannelProperty]; ok {
		channel.(chan bool) <- false
	}
	return nil
}

// AddNewOrchestrationEvent implements backend.Backend and sends the event e to the workflow actor identified by id.
func (abe *Actors) AddNewOrchestrationEvent(ctx context.Context, id api.InstanceID, e *backend.HistoryEvent) error {
	data, err := proto.Marshal(e)
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

// GetOrchestrationRuntimeState implements backend.Backend
func (abe *Actors) GetOrchestrationRuntimeState(ctx context.Context, owi *backend.OrchestrationWorkItem) (*backend.OrchestrationRuntimeState, error) {
	state, err := abe.loadInternalState(ctx, owi.InstanceID)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, api.ErrInstanceNotFound
	}
	runtimeState := runtimestate.NewOrchestrationRuntimeState(string(owi.InstanceID), state.CustomStatus, state.History)
	return runtimeState, nil
}

func (abe *Actors) WatchOrchestrationRuntimeStatus(ctx context.Context, id api.InstanceID, ch chan<- *backend.OrchestrationMetadata) error {
	log.Debugf("Actor backend streaming OrchestrationRuntimeStatus %s", id)

	engine, err := abe.actors.Engine(ctx)
	if err != nil {
		return err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.WaitForRuntimeStatus).
		WithActor(abe.workflowActorType, string(id)).
		WithContentType(invokev1.ProtobufContentType)

	stream := make(chan *internalsv1pb.InternalInvokeResponse, 5)

	for {
		err = concurrency.NewRunnerManager(
			func(ctx context.Context) error {
				return engine.CallStream(ctx, req, stream)
			},
			func(ctx context.Context) error {
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case val := <-stream:
						var meta backend.OrchestrationMetadata
						if perr := val.GetMessage().GetData().UnmarshalTo(&meta); perr != nil {
							log.Errorf("Failed to unmarshal orchestration metadata: %s", perr)
							return perr
						}
						select {
						case ch <- &meta:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
				}
			},
		).Run(ctx)
		if err != nil {
			status, ok := status.FromError(err)
			if ok && status.Code() == codes.Canceled {
				return nil
			}

			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
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
	return "dapr.actors/v1"
}

func (abe *Actors) loadInternalState(ctx context.Context, id api.InstanceID) (*state.State, error) {
	astate, err := abe.actors.State(ctx)
	if err != nil {
		return nil, err
	}

	// actor id is workflow instance id
	state, err := state.LoadWorkflowState(ctx, astate, string(id), state.Options{
		AppID:             abe.appID,
		WorkflowActorType: abe.workflowActorType,
		ActivityActorType: abe.activityActorType,
	})
	if err != nil {
		return nil, err
	}
	if state == nil {
		// No such state exists in the state store
		return nil, nil
	}
	return state, nil
}

// NextOrchestrationWorkItem implements backend.Backend
func (abe *Actors) NextOrchestrationWorkItem(ctx context.Context) (*backend.OrchestrationWorkItem, error) {
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

// NextActivityWorkItem implements backend.Backend
func (abe *Actors) NextActivityWorkItem(ctx context.Context) (*backend.ActivityWorkItem, error) {
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

func (abe *Actors) ActivityActorType() string {
	return abe.activityActorType
}
