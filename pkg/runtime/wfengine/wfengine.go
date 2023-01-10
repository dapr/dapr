/*
Copyright 2022 The Dapr Authors
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
	"time"

	"github.com/microsoft/durabletask-go/backend"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/kit/logger"
)

const (
	WorkflowActorType = actors.InternalActorTypePrefix + "wfengine.workflow"
	ActivityActorType = actors.InternalActorTypePrefix + "wfengine.activity"
)

type WorkflowEngine struct {
	backend  *actorBackend
	executor backend.Executor

	workflowActor *workflowActor
	activityActor *activityActor
}

var (
	wfLogger            = logger.NewLogger("dapr.runtime.wfengine")
	errExecutionAborted = errors.New("execution aborted")
)

func IsWorkflowRequest(path string) bool {
	return backend.IsDurableTaskGrpcRequest(path)
}

func NewWorkflowEngine() *WorkflowEngine {
	be := NewActorBackend()
	engine := &WorkflowEngine{
		backend:       be,
		workflowActor: NewWorkflowActor(be),
		activityActor: NewActivityActor(be),
	}
	return engine
}

// InternalActors returns a map of internal actors that are used to implement workflows
func (wfe *WorkflowEngine) InternalActors() map[string]actors.InternalActor {
	internalActors := make(map[string]actors.InternalActor)
	internalActors[WorkflowActorType] = wfe.workflowActor
	internalActors[ActivityActorType] = wfe.activityActor
	return internalActors
}

func (wfe *WorkflowEngine) ConfigureGrpc(grpcServer *grpc.Server) {
	wfLogger.Info("configuring workflow engine gRPC endpoint")
	wfe.ConfigureExecutor(func(be backend.Backend) backend.Executor {
		return backend.NewGrpcExecutor(grpcServer, wfe.backend, wfLogger)
	})
}

func (wfe *WorkflowEngine) ConfigureExecutor(factory func(be backend.Backend) backend.Executor) {
	wfe.executor = factory(wfe.backend)
}

func (wfe *WorkflowEngine) SetActorRuntime(actorRuntime actors.Actors) error {
	wfLogger.Info("configuring workflow engine with actors backend")
	for actorType, actor := range wfe.InternalActors() {
		if err := actorRuntime.RegisterInternalActor(context.TODO(), actorType, actor); err != nil {
			return fmt.Errorf("failed to register workflow actor %s: %w", actorType, err)
		}
	}

	wfLogger.Infof("workflow actors registered, workflow engine is ready")
	wfe.backend.SetActorRuntime(actorRuntime)

	return nil
}

// DisableActorCaching turns off the default caching done by the workflow and activity actors.
// This method is primarily intended to be used for testing to ensure correct behavior
// when actors are newly activated on nodes, but without requiring the actor to actually
// go through activation.
func (wfe *WorkflowEngine) DisableActorCaching(disable bool) {
	wfe.workflowActor.cachingDisabled = disable
	wfe.activityActor.cachingDisabled = disable
}

// SetWorkflowTimeout allows configuring a default timeout for workflow execution steps.
// If the timeout is exceeded, the workflow execution step will be abandoned and retried.
// Note that this timeout is for a non-blocking step in the workflow (which is expected
// to always complete almost immediately) and not for the end-to-end workflow execution.
func (wfe *WorkflowEngine) SetWorkflowTimeout(timeout time.Duration) {
	wfe.workflowActor.defaultTimeout = timeout
}

// SetActivityTimeout allows configuring a default timeout for activity executions.
// If the timeout is exceeded, the activity execution will be abandoned and retried.
func (wfe *WorkflowEngine) SetActivityTimeout(timeout time.Duration) {
	wfe.activityActor.defaultTimeout = timeout
}

// SetActorReminderInterval sets the amount of delay between internal retries for
// workflow and activity actors. This impacts how long it takes for an operation to
// restart itself after a timeout or a process failure is encountered while running.
func (wfe *WorkflowEngine) SetActorReminderInterval(interval time.Duration) {
	wfe.workflowActor.reminderInterval = interval
	wfe.activityActor.reminderInterval = interval
}

func (wfe *WorkflowEngine) Start(ctx context.Context) error {
	if wfe.backend.actors == nil {
		return errors.New("backend actor runtime is not configured")
	} else if wfe.executor == nil {
		return errors.New("grpc executor is not yet configured")
	}

	// TODO: Determine whether a more dynamic parallelism configuration is necessary.
	parallelismOpts := backend.WithMaxParallelism(100)

	orchestrationWorker := backend.NewOrchestrationWorker(wfe.backend, wfe.executor, wfLogger, parallelismOpts)
	activityWorker := backend.NewActivityTaskWorker(wfe.backend, wfe.executor, wfLogger, parallelismOpts)
	taskHubWorker := backend.NewTaskHubWorker(wfe.backend, orchestrationWorker, activityWorker, wfLogger)
	if err := taskHubWorker.Start(ctx); err != nil {
		return fmt.Errorf("failed to start workflow engine: %w", err)
	}

	wfLogger.Info("workflow engine started")
	return nil
}
