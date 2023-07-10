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
package wfengine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors/core"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

// workflowScheduler is an interface for pushing work items into the backend
type workflowScheduler interface {
	ScheduleWorkflow(ctx context.Context, wi *backend.OrchestrationWorkItem) error
	ScheduleActivity(ctx context.Context, wi *backend.ActivityWorkItem) error
}

type actorBackend struct {
	actors                    core.Actors
	orchestrationWorkItemChan chan *backend.OrchestrationWorkItem
	activityWorkItemChan      chan *backend.ActivityWorkItem
	startedOnce               sync.Once
	config                    wfConfig
}

func NewActorBackend(engine *WorkflowEngine) *actorBackend {
	return &actorBackend{
		orchestrationWorkItemChan: make(chan *backend.OrchestrationWorkItem),
		activityWorkItemChan:      make(chan *backend.ActivityWorkItem),
		config:                    engine.config,
	}
}

func (be *actorBackend) SetActorRuntime(actors core.Actors) {
	be.actors = actors
}

// ScheduleActivity implements workflowScheduler
func (be *actorBackend) ScheduleActivity(ctx context.Context, wi *backend.ActivityWorkItem) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case be.activityWorkItemChan <- wi:
		return nil
	}
}

// ScheduleWorkflow implements workflowScheduler
func (be *actorBackend) ScheduleWorkflow(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case be.orchestrationWorkItemChan <- wi:
		return nil
	}
}

// CreateOrchestrationInstance implements backend.Backend and creates a new workflow instance.
//
// Internally, creating a workflow instance also creates a new actor with the same ID. The create
// request is saved into the actor's "inbox" and then executed via a reminder thread. If the app is
// scaled out across multiple replicas, the actor might get assigned to a replicas other than this one.
func (be *actorBackend) CreateOrchestrationInstance(ctx context.Context, e *backend.HistoryEvent) error {
	if err := be.validateConfiguration(); err != nil {
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

	eventData, err := backend.MarshalHistoryEvent(e)
	if err != nil {
		return err
	}

	// Invoke the well-known workflow actor directly, which will be created by this invocation
	// request. Note that this request goes directly to the actor runtime, bypassing the API layer.
	req := invokev1.
		NewInvokeMethodRequest(CreateWorkflowInstanceMethod).
		WithActor(be.config.workflowActorType, workflowInstanceID).
		WithRawDataBytes(eventData).
		WithContentType(invokev1.OctetStreamContentType)
	defer req.Close()

	resp, err := be.actors.Call(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Close()
	return nil
}

// GetOrchestrationMetadata implements backend.Backend
func (be *actorBackend) GetOrchestrationMetadata(ctx context.Context, id api.InstanceID) (*api.OrchestrationMetadata, error) {
	// Invoke the corresponding actor, which internally stores its own workflow metadata
	req := invokev1.
		NewInvokeMethodRequest(GetWorkflowMetadataMethod).
		WithActor(be.config.workflowActorType, string(id)).
		WithContentType(invokev1.OctetStreamContentType)
	defer req.Close()

	res, err := be.actors.Call(ctx, req)
	if err != nil {
		return nil, err
	}

	defer res.Close()
	data, err := res.RawDataFull()
	if err != nil {
		return nil, fmt.Errorf("failed to read the internal actor response: %w", err)
	}
	if len(data) == 0 {
		return nil, api.ErrInstanceNotFound
	}
	var metadata api.OrchestrationMetadata
	if err := core.DecodeInternalActorData(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to decode the internal actor response: %w", err)
	}
	return &metadata, nil
}

// AbandonActivityWorkItem implements backend.Backend. It gets called by durabletask-go when there is
// an unexpected failure in the workflow activity execution pipeline.
func (*actorBackend) AbandonActivityWorkItem(ctx context.Context, wi *backend.ActivityWorkItem) error {
	wfLogger.Warnf("%s: aborting activity execution (::%d)", wi.InstanceID, wi.NewEvent.EventId)

	// Sending false signals the waiting activity actor to abort the activity execution.
	if channel, ok := wi.Properties[CallbackChannelProperty]; ok {
		channel.(chan bool) <- false
	}
	return nil
}

// AbandonOrchestrationWorkItem implements backend.Backend. It gets called by durabletask-go when there is
// an unexpected failure in the workflow orchestration execution pipeline.
func (*actorBackend) AbandonOrchestrationWorkItem(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
	wfLogger.Warnf("%s: aborting workflow execution", wi.InstanceID)

	// Sending false signals the waiting workflow actor to abort the workflow execution.
	if channel, ok := wi.Properties[CallbackChannelProperty]; ok {
		channel.(chan bool) <- false
	}
	return nil
}

// AddNewOrchestrationEvent implements backend.Backend and sends the event e to the workflow actor identified by id.
func (be *actorBackend) AddNewOrchestrationEvent(ctx context.Context, id api.InstanceID, e *backend.HistoryEvent) error {
	data, err := backend.MarshalHistoryEvent(e)
	if err != nil {
		return err
	}

	// Send the event to the corresponding workflow actor, which will store it in its event inbox.
	req := invokev1.
		NewInvokeMethodRequest(AddWorkflowEventMethod).
		WithActor(be.config.workflowActorType, string(id)).
		WithRawDataBytes(data).
		WithContentType(invokev1.OctetStreamContentType)
	defer req.Close()

	resp, err := be.actors.Call(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Close()
	return nil
}

// CompleteActivityWorkItem implements backend.Backend
func (*actorBackend) CompleteActivityWorkItem(ctx context.Context, wi *backend.ActivityWorkItem) error {
	// Sending true signals the waiting activity actor to complete the execution normally.
	wi.Properties[CallbackChannelProperty].(chan bool) <- true
	return nil
}

// CompleteOrchestrationWorkItem implements backend.Backend
func (*actorBackend) CompleteOrchestrationWorkItem(ctx context.Context, wi *backend.OrchestrationWorkItem) error {
	// Sending true signals the waiting workflow actor to complete the execution normally.
	wi.Properties[CallbackChannelProperty].(chan bool) <- true
	return nil
}

// CreateTaskHub implements backend.Backend
func (*actorBackend) CreateTaskHub(context.Context) error {
	return nil
}

// DeleteTaskHub implements backend.Backend
func (*actorBackend) DeleteTaskHub(context.Context) error {
	return errors.New("not supported")
}

// GetActivityWorkItem implements backend.Backend
func (be *actorBackend) GetActivityWorkItem(ctx context.Context) (*backend.ActivityWorkItem, error) {
	// Wait for the workflow actor to signal us with some work to do
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case wi := <-be.activityWorkItemChan:
		return wi, nil
	}
}

// GetOrchestrationRuntimeState implements backend.Backend
func (*actorBackend) GetOrchestrationRuntimeState(context.Context, *backend.OrchestrationWorkItem) (*backend.OrchestrationRuntimeState, error) {
	return nil, errors.New("not supported")
}

// GetOrchestrationWorkItem implements backend.Backend
func (be *actorBackend) GetOrchestrationWorkItem(ctx context.Context) (*backend.OrchestrationWorkItem, error) {
	// Wait for the workflow actor to signal us with some work to do
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case wi := <-be.orchestrationWorkItemChan:
		return wi, nil
	}
}

// PurgeOrchestrationState deletes all saved state for the specific orchestration instance.
func (be *actorBackend) PurgeOrchestrationState(ctx context.Context, id api.InstanceID) error {
	req := invokev1.
		NewInvokeMethodRequest(PurgeWorkflowStateMethod).
		WithActor(be.config.workflowActorType, string(id))
	defer req.Close()

	resp, err := be.actors.Call(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Close()
	return nil
}

// Start implements backend.Backend
func (be *actorBackend) Start(ctx context.Context) error {
	var err error
	be.startedOnce.Do(func() {
		err = be.validateConfiguration()
	})
	return err
}

// Stop implements backend.Backend
func (*actorBackend) Stop(context.Context) error {
	return nil
}

// String displays the type information
func (be *actorBackend) String() string {
	return fmt.Sprintf("dapr.actors/v1-alpha")
}

func (be *actorBackend) validateConfiguration() error {
	if be.actors == nil {
		return errors.New("actor runtime has not been configured")
	}
	return nil
}
