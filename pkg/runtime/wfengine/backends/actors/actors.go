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
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"

	"github.com/dapr/dapr/pkg/actors"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets/workflow"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/local"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var log = logger.NewLogger("dapr.wfengine.backend.actors")

const (
	WorkflowNameLabelKey = "workflow"
	ActivityNameLabelKey = "activity"
	ExecutorNameLabelKey = "executor"
	ActorTypePrefix      = "dapr.internal."
)

type Options struct {
	AppID              string
	Namespace          string
	Actors             actors.Interface
	Resiliency         resiliency.Provider
	SchedulerReminders bool
	EventSink          orchestrator.EventSink
	// experimental feature
	// enabling this will use the cluster tasks backend for pending tasks, instead of the default local implementation
	// the cluster tasks backend uses actors to share the state of pending tasks
	// allowing to deploy multiple daprd replicas and expose them through a loadbalancer
	EnableClusteredDeployment bool
}

type Actors struct {
	appID             string
	namespace         string
	workflowActorType string
	activityActorType string
	executorActorType string

	enableClusteredDeployment bool
	pendingTasksBackend       PendingTasksBackend
	defaultReminderInterval   *time.Duration
	resiliency                resiliency.Provider
	actors                    actors.Interface
	schedulerReminders        bool
	eventSink                 orchestrator.EventSink

	orchestrationWorkItemChan chan *backend.OrchestrationWorkItem
	activityWorkItemChan      chan *backend.ActivityWorkItem

	stopped atomic.Bool
}

func New(opts Options) *Actors {
	var pendingTasksBackend PendingTasksBackend = local.NewTasksBackend()
	if opts.EnableClusteredDeployment {
		pendingTasksBackend = NewClusterTasksBackend(ClusterTasksBackendOptions{
			Actors:            opts.Actors,
			ExecutorActorType: ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + ExecutorNameLabelKey,
		})
	}
	return &Actors{
		appID:                     opts.AppID,
		namespace:                 opts.Namespace,
		workflowActorType:         ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + WorkflowNameLabelKey,
		activityActorType:         ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + ActivityNameLabelKey,
		executorActorType:         ActorTypePrefix + opts.Namespace + utils.DotDelimiter + opts.AppID + utils.DotDelimiter + ExecutorNameLabelKey,
		actors:                    opts.Actors,
		resiliency:                opts.Resiliency,
		schedulerReminders:        opts.SchedulerReminders,
		pendingTasksBackend:       pendingTasksBackend,
		enableClusteredDeployment: opts.EnableClusteredDeployment,
		orchestrationWorkItemChan: make(chan *backend.OrchestrationWorkItem, 1),
		activityWorkItemChan:      make(chan *backend.ActivityWorkItem, 1),
		eventSink:                 opts.EventSink,
	}
}

func (abe *Actors) RegisterActors(ctx context.Context) error {
	atable, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	actorTypeBuilder := common.NewActorTypeBuilder(abe.namespace)
	oopts := orchestrator.Options{
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
		ActorTypeBuilder:   actorTypeBuilder,
	}

	aopts := activity.Options{
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
		ActorTypeBuilder:   actorTypeBuilder,
	}

	workflowFactory, activityFactory, err := workflow.Factories(ctx, oopts, aopts)
	if err != nil {
		return err
	}

	factories := []table.ActorTypeFactory{
		{
			Factory: workflowFactory,
			Type:    abe.workflowActorType,
		},
		{
			Factory: activityFactory,
			Type:    abe.activityActorType,
		},
	}

	if abe.enableClusteredDeployment {
		executorFactory, err := executor.New(ctx, executor.Options{
			ActorType: abe.executorActorType,
			Actors:    abe.actors,
		})
		if err != nil {
			return err
		}
		factories = append(factories, table.ActorTypeFactory{
			Factory: executorFactory,
			Type:    abe.executorActorType,
		})
	}

	atable.RegisterActorTypes(table.RegisterActorTypeOptions{
		Factories: factories,
	})

	return nil
}

func (abe *Actors) UnRegisterActors(ctx context.Context) error {
	table, err := abe.actors.Table(ctx)
	if err != nil {
		return err
	}

	actorTypes := []string{abe.workflowActorType, abe.activityActorType}
	if abe.enableClusteredDeployment {
		actorTypes = append(actorTypes, abe.executorActorType)
	}

	return table.UnRegisterActorTypes(actorTypes...)
}

// RerunWorkflowFromEvent implements backend.Backend and reruns a workflow from
// a specific event ID.
func (abe *Actors) RerunWorkflowFromEvent(ctx context.Context, req *backend.RerunWorkflowFromEventRequest) (api.InstanceID, error) {
	if len(req.GetSourceInstanceID()) == 0 {
		return "", status.Error(codes.InvalidArgument, "rerun workflow source instance ID is required")
	}

	if req.NewInstanceID == nil {
		u, err := uuid.NewRandom()
		if err != nil {
			return "", fmt.Errorf("failed to generate instance ID: %w", err)
		}
		req.NewInstanceID = ptr.Of(u.String())
	}

	if req.GetSourceInstanceID() == req.GetNewInstanceID() {
		return "", status.Error(codes.InvalidArgument, "rerun workflow instance ID must be different from the original instance ID")
	}

	requestBytes, err := proto.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal RerunWorkflowFromEvent: %w", err)
	}

	areq := internalsv1pb.NewInternalInvokeRequest(todo.ForkWorkflowHistory).
		WithActor(abe.workflowActorType, req.GetSourceInstanceID()).
		WithData(requestBytes).
		WithContentType(invokev1.ProtobufContentType)

	engine, err := abe.actors.Router(ctx)
	if err != nil {
		return "", err
	}

	_, err = engine.Call(ctx, areq)
	if err != nil {
		return "", err
	}

	return api.InstanceID(req.GetNewInstanceID()), nil
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

	router, err := abe.actors.Router(ctx)
	if err != nil {
		return err
	}

	err = backoff.Retry(func() error {
		_, eerr := router.Call(ctx, req)
		status, ok := status.FromError(eerr)
		if ok && status.Code() == codes.FailedPrecondition {
			return eerr
		}
		if errors.Is(eerr, actorerrors.ErrCreatingActor) {
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

	router, err := abe.actors.Router(ctx)
	if err != nil {
		return err
	}

	start := time.Now()
	_, err = router.Call(ctx, req)
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

func (abe *Actors) WatchOrchestrationRuntimeStatus(ctx context.Context, id api.InstanceID, condition func(*backend.OrchestrationMetadata) bool) error {
	log.Debugf("Actor backend streaming OrchestrationRuntimeStatus %s", id)

	router, err := abe.actors.Router(ctx)
	if err != nil {
		return err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(todo.WaitForRuntimeStatus).
		WithActor(abe.workflowActorType, string(id)).
		WithContentType(invokev1.ProtobufContentType)

	err = router.CallStream(ctx, req, func(resp *internalsv1pb.InternalInvokeResponse) (bool, error) {
		var meta backend.OrchestrationMetadata
		if perr := resp.GetMessage().GetData().UnmarshalTo(&meta); perr != nil {
			log.Errorf("Failed to unmarshal orchestration metadata: %s", perr)
			return false, perr
		}

		return condition(&meta), nil
	})
	if err != nil {
		return err
	}

	return nil
}

// PurgeOrchestrationState deletes all saved state for the specific orchestration instance.
func (abe *Actors) PurgeOrchestrationState(ctx context.Context, id api.InstanceID) error {
	req := internalsv1pb.
		NewInternalInvokeRequest(todo.PurgeWorkflowStateMethod).
		WithActor(abe.workflowActorType, string(id))

	router, err := abe.actors.Router(ctx)
	if err != nil {
		return err
	}

	start := time.Now()
	_, err = router.Call(ctx, req)
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
	abe.stopped.Store(false)
	return nil
}

// Stop implements backend.Backend
func (abe *Actors) Stop(context.Context) error {
	abe.stopped.Store(true)
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

// CancelActivityTask implements backend.Backend.
func (abe *Actors) CancelActivityTask(ctx context.Context, instanceID api.InstanceID, taskID int32) error {
	return abe.callWithBackoff(ctx, func() error {
		return abe.pendingTasksBackend.CancelActivityTask(ctx, instanceID, taskID)
	})
}

// CancelOrchestratorTask implements backend.Backend.
func (abe *Actors) CancelOrchestratorTask(ctx context.Context, instanceID api.InstanceID) error {
	return abe.callWithBackoff(ctx, func() error {
		return abe.pendingTasksBackend.CancelOrchestratorTask(ctx, instanceID)
	})
}

// CompleteActivityTask implements backend.Backend.
func (abe *Actors) CompleteActivityTask(ctx context.Context, response *protos.ActivityResponse) error {
	return abe.callWithBackoff(ctx, func() error {
		return abe.pendingTasksBackend.CompleteActivityTask(ctx, response)
	})
}

// CompleteOrchestratorTask implements backend.Backend.
func (abe *Actors) CompleteOrchestratorTask(ctx context.Context, response *protos.OrchestratorResponse) error {
	return abe.callWithBackoff(ctx, func() error {
		return abe.pendingTasksBackend.CompleteOrchestratorTask(ctx, response)
	})
}

func (abe *Actors) callWithBackoff(ctx context.Context, fn func() error) error {
	return backoff.Retry(func() error {
		err := fn()
		if err != nil && ctx.Err() == nil {
			log.Warnf("error completing activity task: %v, retrying...", err)
		}
		if abe.stopped.Load() {
			return backoff.Permanent(err)
		}
		return err
	}, backoff.WithContext(
		backoff.NewExponentialBackOff(
			backoff.WithMaxInterval(3*time.Second),
			backoff.WithRandomizationFactor(0.3),
		), ctx))
}

// WaitForActivityCompletion implements backend.Backend.
func (abe *Actors) WaitForActivityCompletion(ctx context.Context, request *protos.ActivityRequest) (*protos.ActivityResponse, error) {
	return abe.pendingTasksBackend.WaitForActivityCompletion(ctx, request)
}

// WaitForOrchestratorCompletion implements backend.Backend.
func (abe *Actors) WaitForOrchestratorCompletion(ctx context.Context, request *protos.OrchestratorRequest) (*protos.OrchestratorResponse, error) {
	return abe.pendingTasksBackend.WaitForOrchestratorCompletion(ctx, request)
}
