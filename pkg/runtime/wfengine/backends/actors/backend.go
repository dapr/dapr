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
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors"
	wfbe "github.com/dapr/dapr/pkg/components/wfbackend"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

var (
	wfLogger            = logger.NewLogger("dapr.wfengine.backend.actors")
	errExecutionAborted = errors.New("execution aborted")
)

const (
	defaultNamespace     = "default"
	WorkflowNameLabelKey = "workflow"
	ActivityNameLabelKey = "activity"
)

// actorsBackendConfig is the configuration for the workflow engine's actors backend
type actorsBackendConfig struct {
	AppID             string
	workflowActorType string
	activityActorType string
}

// NewActorsBackendConfig creates a new workflow engine configuration
func NewActorsBackendConfig(appID string) actorsBackendConfig {
	return actorsBackendConfig{
		AppID:             appID,
		workflowActorType: actors.InternalActorTypePrefix + utils.GetNamespaceOrDefault(defaultNamespace) + utils.DotDelimiter + appID + utils.DotDelimiter + WorkflowNameLabelKey,
		activityActorType: actors.InternalActorTypePrefix + utils.GetNamespaceOrDefault(defaultNamespace) + utils.DotDelimiter + appID + utils.DotDelimiter + ActivityNameLabelKey,
	}
}

// String implements fmt.Stringer and is primarily used for debugging purposes.
func (c *actorsBackendConfig) String() string {
	if c == nil {
		return "(nil)"
	}
	return fmt.Sprintf("AppID='%s' workflowActorType='%s' activityActorType='%s'", c.AppID, c.workflowActorType, c.activityActorType)
}

type ActorBackend struct {
	orchestrationWorkItemChan chan *backend.OrchestrationWorkItem
	activityWorkItemChan      chan *backend.ActivityWorkItem
	startedOnce               sync.Once
	config                    actorsBackendConfig
	activityActorOpts         activityActorOpts
	workflowActorOpts         workflowActorOpts

	actorRuntime  actors.ActorRuntime
	actorsReady   atomic.Bool
	actorsReadyCh chan struct{}
}

func NewActorBackend(md wfbe.Metadata, _ logger.Logger) (backend.Backend, error) {
	backendConfig := NewActorsBackendConfig(md.AppID)

	// These channels are used by actors to call into this backend object
	orchestrationWorkItemChan := make(chan *backend.OrchestrationWorkItem)
	activityWorkItemChan := make(chan *backend.ActivityWorkItem)

	return &ActorBackend{
		orchestrationWorkItemChan: orchestrationWorkItemChan,
		activityWorkItemChan:      activityWorkItemChan,
		config:                    backendConfig,
		actorsReadyCh:             make(chan struct{}),
	}, nil
}

// getWorkflowScheduler returns a workflowScheduler func that sends an orchestration work item to the Durable Task Framework.
func getWorkflowScheduler(orchestrationWorkItemChan chan *backend.OrchestrationWorkItem) workflowScheduler {
	return func(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
		wfLogger.Debugf("%s: scheduling workflow execution with durabletask engine", wi.InstanceID)
		select {
		case <-ctx.Done(): // <-- engine is shutting down or a caller timeout expired
			return ctx.Err()
		case orchestrationWorkItemChan <- wi: // blocks until the engine is ready to process the work item
			return nil
		}
	}
}

// getActivityScheduler returns an activityScheduler func that sends an activity work item to the Durable Task Framework.
func getActivityScheduler(activityWorkItemChan chan *backend.ActivityWorkItem) activityScheduler {
	return func(ctx context.Context, wi *backend.ActivityWorkItem) error {
		wfLogger.Debugf(
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

// InternalActors returns a map of internal actors that are used to implement workflows
func (abe *ActorBackend) GetInternalActorsMap() map[string]actors.InternalActorFactory {
	internalActors := make(map[string]actors.InternalActorFactory)
	internalActors[abe.config.workflowActorType] = NewWorkflowActor(getWorkflowScheduler(abe.orchestrationWorkItemChan), abe.config, &abe.workflowActorOpts)
	internalActors[abe.config.activityActorType] = NewActivityActor(getActivityScheduler(abe.activityWorkItemChan), abe.config, &abe.activityActorOpts)
	return internalActors
}

func (abe *ActorBackend) SetActorRuntime(ctx context.Context, actorRuntime actors.ActorRuntime) {
	abe.actorRuntime = actorRuntime
	if abe.actorsReady.CompareAndSwap(false, true) {
		close(abe.actorsReadyCh)
	}
}

func (abe *ActorBackend) RegisterActor(ctx context.Context) error {
	if abe.actorRuntime != nil {
		for actorType, actor := range abe.GetInternalActorsMap() {
			err := abe.actorRuntime.RegisterInternalActor(ctx, actorType, actor, time.Minute*1)
			if err != nil {
				return fmt.Errorf("failed to register workflow actor %s: %w", actorType, err)
			}
		}
	}

	return nil
}

// CreateOrchestrationInstance implements backend.Backend and creates a new workflow instance.
//
// Internally, creating a workflow instance also creates a new actor with the same ID. The create
// request is saved into the actor's "inbox" and then executed via a reminder thread. If the app is
// scaled out across multiple replicas, the actor might get assigned to a replicas other than this one.
func (abe *ActorBackend) CreateOrchestrationInstance(ctx context.Context, e *backend.HistoryEvent, opts ...backend.OrchestrationIdReusePolicyOptions) error {
	if err := abe.validateConfiguration(); err != nil {
		return err
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

	eventData, err := backend.MarshalHistoryEvent(e)
	if err != nil {
		return err
	}

	requestBytes, err := json.Marshal(CreateWorkflowInstanceRequest{
		Policy:          policy,
		StartEventBytes: eventData,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal CreateWorkflowInstanceRequest: %w", err)
	}

	// Invoke the well-known workflow actor directly, which will be created by this invocation request.
	// Note that this request goes directly to the actor runtime, bypassing the API layer.
	req := internalsv1pb.NewInternalInvokeRequest(CreateWorkflowInstanceMethod).
		WithActor(abe.config.workflowActorType, workflowInstanceID).
		WithData(requestBytes).
		WithContentType(invokev1.JSONContentType)
	start := time.Now()
	_, err = abe.actorRuntime.Call(ctx, req)
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
func (abe *ActorBackend) GetOrchestrationMetadata(ctx context.Context, id api.InstanceID) (*api.OrchestrationMetadata, error) {
	// Invoke the corresponding actor, which internally stores its own workflow metadata
	req := internalsv1pb.
		NewInternalInvokeRequest(GetWorkflowMetadataMethod).
		WithActor(abe.config.workflowActorType, string(id)).
		WithContentType(invokev1.OctetStreamContentType)

	start := time.Now()
	res, err := abe.actorRuntime.Call(ctx, req)
	elapsed := diag.ElapsedSince(start)
	if err != nil {
		// failed request to GET workflow Information, record count and latency metrics.
		diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.GetWorkflow, diag.StatusFailed, elapsed)
		return nil, err
	}
	// successful request to GET workflow information, record count and latency metrics.
	diag.DefaultWorkflowMonitoring.WorkflowOperationEvent(ctx, diag.GetWorkflow, diag.StatusSuccess, elapsed)

	var metadata api.OrchestrationMetadata
	err = actors.DecodeInternalActorData(bytes.NewReader(res.GetMessage().GetData().GetValue()), &metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to decode the internal actor response: %w", err)
	}
	return &metadata, nil
}

// AbandonActivityWorkItem implements backend.Backend. It gets called by durabletask-go when there is
// an unexpected failure in the workflow activity execution pipeline.
func (*ActorBackend) AbandonActivityWorkItem(ctx context.Context, wi *backend.ActivityWorkItem) error {
	wfLogger.Warnf("%s: aborting activity execution (::%d)", wi.InstanceID, wi.NewEvent.GetEventId())

	// Sending false signals the waiting activity actor to abort the activity execution.
	if channel, ok := wi.Properties[CallbackChannelProperty]; ok {
		channel.(chan bool) <- false
	}
	return nil
}

// AbandonOrchestrationWorkItem implements backend.Backend. It gets called by durabletask-go when there is
// an unexpected failure in the workflow orchestration execution pipeline.
func (*ActorBackend) AbandonOrchestrationWorkItem(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
	wfLogger.Warnf("%s: aborting workflow execution", wi.InstanceID)

	// Sending false signals the waiting workflow actor to abort the workflow execution.
	if channel, ok := wi.Properties[CallbackChannelProperty]; ok {
		channel.(chan bool) <- false
	}
	return nil
}

// AddNewOrchestrationEvent implements backend.Backend and sends the event e to the workflow actor identified by id.
func (abe *ActorBackend) AddNewOrchestrationEvent(ctx context.Context, id api.InstanceID, e *backend.HistoryEvent) error {
	data, err := backend.MarshalHistoryEvent(e)
	if err != nil {
		return err
	}

	// Send the event to the corresponding workflow actor, which will store it in its event inbox.
	req := internalsv1pb.
		NewInternalInvokeRequest(AddWorkflowEventMethod).
		WithActor(abe.config.workflowActorType, string(id)).
		WithData(data).
		WithContentType(invokev1.OctetStreamContentType)

	start := time.Now()
	_, err = abe.actorRuntime.Call(ctx, req)
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
func (*ActorBackend) CompleteActivityWorkItem(ctx context.Context, wi *backend.ActivityWorkItem) error {
	// Sending true signals the waiting activity actor to complete the execution normally.
	wi.Properties[CallbackChannelProperty].(chan bool) <- true
	return nil
}

// CompleteOrchestrationWorkItem implements backend.Backend
func (*ActorBackend) CompleteOrchestrationWorkItem(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
	// Sending true signals the waiting workflow actor to complete the execution normally.
	wi.Properties[CallbackChannelProperty].(chan bool) <- true
	return nil
}

// CreateTaskHub implements backend.Backend
func (*ActorBackend) CreateTaskHub(context.Context) error {
	return nil
}

// DeleteTaskHub implements backend.Backend
func (*ActorBackend) DeleteTaskHub(context.Context) error {
	return errors.New("not supported")
}

// GetActivityWorkItem implements backend.Backend
func (abe *ActorBackend) GetActivityWorkItem(ctx context.Context) (*backend.ActivityWorkItem, error) {
	// Wait for the activity actor to signal us with some work to do
	wfLogger.Debug("Actor backend is waiting for an activity actor to schedule an invocation.")
	select {
	case wi := <-abe.activityWorkItemChan:
		wfLogger.Debugf(
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
func (abe *ActorBackend) GetOrchestrationRuntimeState(ctx context.Context, owi *backend.OrchestrationWorkItem) (*backend.OrchestrationRuntimeState, error) {
	// Invoke the corresponding actor, which internally stores its own workflow state.
	req := internalsv1pb.
		NewInternalInvokeRequest(GetWorkflowStateMethod).
		WithActor(abe.config.workflowActorType, string(owi.InstanceID)).
		WithContentType(invokev1.OctetStreamContentType)

	res, err := abe.actorRuntime.Call(ctx, req)
	if err != nil {
		return nil, err
	}
	wfState := &workflowState{}
	err = wfState.DecodeWorkflowState(res.GetMessage().GetData().GetValue())
	if err != nil {
		return nil, fmt.Errorf("failed to decode the internal actor response: %w", err)
	}
	runtimeState := getRuntimeState(string(owi.InstanceID), wfState)
	return runtimeState, nil
}

// GetOrchestrationWorkItem implements backend.Backend
func (abe *ActorBackend) GetOrchestrationWorkItem(ctx context.Context) (*backend.OrchestrationWorkItem, error) {
	// Wait for the workflow actor to signal us with some work to do
	wfLogger.Debug("Actor backend is waiting for a workflow actor to schedule an invocation.")
	select {
	case wi := <-abe.orchestrationWorkItemChan:
		wfLogger.Debugf("Actor backend received a workflow task for workflow '%s'.", wi.InstanceID)
		return wi, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// PurgeOrchestrationState deletes all saved state for the specific orchestration instance.
func (abe *ActorBackend) PurgeOrchestrationState(ctx context.Context, id api.InstanceID) error {
	req := internalsv1pb.
		NewInternalInvokeRequest(PurgeWorkflowStateMethod).
		WithActor(abe.config.workflowActorType, string(id))

	start := time.Now()
	_, err := abe.actorRuntime.Call(ctx, req)
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
func (abe *ActorBackend) Start(ctx context.Context) error {
	var err error
	abe.startedOnce.Do(func() {
		err = abe.validateConfiguration()
	})
	return err
}

// Stop implements backend.Backend
func (*ActorBackend) Stop(context.Context) error {
	return nil
}

// String displays the type information
func (abe *ActorBackend) String() string {
	return "dapr.actors/v1-beta"
}

func (abe *ActorBackend) validateConfiguration() error {
	if abe.actorRuntime == nil {
		return errors.New("actor runtime has not been configured")
	}
	return nil
}

// WaitForActorsReady blocks until the actor runtime is set in the object (or until the context is canceled).
func (abe *ActorBackend) WaitForActorsReady(ctx context.Context) {
	select {
	case <-ctx.Done():
		// No-op
	case <-abe.actorsReadyCh:
		// No-op
	}
}

// DisableActorCaching turns off the default caching done by the workflow and activity actors.
// This method is primarily intended to be used for testing to ensure correct behavior
// when actors are newly activated on nodes, but without requiring the actor to actually
// go through activation.
func (abe *ActorBackend) DisableActorCaching(disable bool) {
	abe.workflowActorOpts.cachingDisabled = disable
	abe.activityActorOpts.cachingDisabled = disable
}

// SetWorkflowTimeout allows configuring a default timeout for workflow execution steps.
// If the timeout is exceeded, the workflow execution step will be abandoned and retried.
// Note that this timeout is for a non-blocking step in the workflow (which is expected
// to always complete almost immediately) and not for the end-to-end workflow execution.
func (abe *ActorBackend) SetWorkflowTimeout(timeout time.Duration) {
	abe.workflowActorOpts.defaultTimeout = timeout
}

// SetActivityTimeout allows configuring a default timeout for activity executions.
// If the timeout is exceeded, the activity execution will be abandoned and retried.
func (abe *ActorBackend) SetActivityTimeout(timeout time.Duration) {
	abe.activityActorOpts.defaultTimeout = timeout
}

// SetActorReminderInterval sets the amount of delay between internal retries for
// workflow and activity actors. This impacts how long it takes for an operation to
// restart itself after a timeout or a process failure is encountered while running.
func (abe *ActorBackend) SetActorReminderInterval(interval time.Duration) {
	abe.workflowActorOpts.reminderInterval = interval
	abe.activityActorOpts.reminderInterval = interval
}
