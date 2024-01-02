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
	"sync/atomic"
	"time"

	"github.com/microsoft/durabletask-go/backend"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/logger"
)

type WorkflowEngine struct {
	IsRunning bool

	backend              *actorBackend
	executor             backend.Executor
	worker               backend.TaskHubWorker
	registerGrpcServerFn func(grpcServer grpc.ServiceRegistrar)

	actorRuntime  actors.ActorRuntime
	actorsReady   atomic.Bool
	actorsReadyCh chan struct{}

	startMutex     sync.Mutex
	disconnectChan chan any
	spec           config.WorkflowSpec
}

const (
	defaultNamespace     = "default"
	WorkflowNameLabelKey = "workflow"
	ActivityNameLabelKey = "activity"
)

var (
	wfLogger            = logger.NewLogger("dapr.runtime.wfengine")
	wfBackendLogger     = logger.NewLogger("wfengine.durabletask.backend")
	errExecutionAborted = errors.New("execution aborted")
)

func IsWorkflowRequest(path string) bool {
	return backend.IsDurableTaskGrpcRequest(path)
}

func NewWorkflowEngine(appID string, spec config.WorkflowSpec) *WorkflowEngine {
	engine := &WorkflowEngine{
		spec:          spec,
		actorsReadyCh: make(chan struct{}),
	}
	be := NewActorBackend(appID)
	engine.backend = be

	return engine
}

// GetInternalActorsMap returns a map of internal actors that are used to implement workflows
func (wfe *WorkflowEngine) GetInternalActorsMap() map[string]actors.InternalActor {
	return wfe.backend.GetInternalActorsMap()
}

func (wfe *WorkflowEngine) RegisterGrpcServer(grpcServer *grpc.Server) {
	wfe.registerGrpcServerFn(grpcServer)
}

func (wfe *WorkflowEngine) ConfigureGrpcExecutor() {
	// Enable lazy auto-starting the worker only when a workflow app connects to fetch work items.
	autoStartCallback := backend.WithOnGetWorkItemsConnectionCallback(func(ctx context.Context) error {
		// NOTE: We don't propagate the context here because that would cause the engine to shut
		//       down when the client disconnects and cancels the passed-in context. Once it starts
		//       up, we want to keep the engine running until the runtime shuts down.
		if err := wfe.Start(context.Background()); err != nil {
			// This can happen if the workflow app connects before the sidecar has finished initializing.
			// The client app is expected to continuously retry until successful.
			return fmt.Errorf("failed to auto-start the workflow engine: %w", err)
		}
		return nil
	})

	// Create a channel that can be used to disconnect the remote client during shutdown.
	wfe.disconnectChan = make(chan any, 1)
	disconnectHelper := backend.WithStreamShutdownChannel(wfe.disconnectChan)

	wfe.executor, wfe.registerGrpcServerFn = backend.NewGrpcExecutor(wfe.backend, wfLogger, autoStartCallback, disconnectHelper)
}

// SetExecutor sets the executor property. This is primarily used for testing.
func (wfe *WorkflowEngine) SetExecutor(fn func(be backend.Backend) backend.Executor) {
	wfe.executor = fn(wfe.backend)
}

func (wfe *WorkflowEngine) SetActorRuntime(actorRuntime actors.ActorRuntime) {
	if actorRuntime != nil {
		wfLogger.Info("Configuring workflow engine with actors backend")
		wfe.actorRuntime = actorRuntime
		wfe.backend.SetActorRuntime(actorRuntime)
	}

	if wfe.actorsReady.CompareAndSwap(false, true) {
		close(wfe.actorsReadyCh)
	}
}

// WaitForActorsReady blocks until the actor runtime is set in the object (or until the context is canceled).
func (wfe *WorkflowEngine) WaitForActorsReady(ctx context.Context) {
	select {
	case <-ctx.Done():
		// No-op
	case <-wfe.actorsReadyCh:
		// No-op
	}
}

// DisableActorCaching turns off the default caching done by the workflow and activity actors.
// This method is primarily intended to be used for testing to ensure correct behavior
// when actors are newly activated on nodes, but without requiring the actor to actually
// go through activation.
func (wfe *WorkflowEngine) DisableActorCaching(disable bool) {
	wfe.backend.workflowActor.cachingDisabled = disable
	wfe.backend.activityActor.cachingDisabled = disable
}

// SetWorkflowTimeout allows configuring a default timeout for workflow execution steps.
// If the timeout is exceeded, the workflow execution step will be abandoned and retried.
// Note that this timeout is for a non-blocking step in the workflow (which is expected
// to always complete almost immediately) and not for the end-to-end workflow execution.
func (wfe *WorkflowEngine) SetWorkflowTimeout(timeout time.Duration) {
	wfe.backend.workflowActor.defaultTimeout = timeout
}

// SetActivityTimeout allows configuring a default timeout for activity executions.
// If the timeout is exceeded, the activity execution will be abandoned and retried.
func (wfe *WorkflowEngine) SetActivityTimeout(timeout time.Duration) {
	wfe.backend.activityActor.defaultTimeout = timeout
}

// SetActorReminderInterval sets the amount of delay between internal retries for
// workflow and activity actors. This impacts how long it takes for an operation to
// restart itself after a timeout or a process failure is encountered while running.
func (wfe *WorkflowEngine) SetActorReminderInterval(interval time.Duration) {
	wfe.backend.workflowActor.reminderInterval = interval
	wfe.backend.activityActor.reminderInterval = interval
}

// SetLogLevel sets the logging level for the workflow engine.
// This function is only intended to be used for testing.
func SetLogLevel(level logger.LogLevel) {
	wfLogger.SetOutputLevel(level)
	wfBackendLogger.SetOutputLevel(level)
}

func (wfe *WorkflowEngine) Start(ctx context.Context) (err error) {
	wfe.WaitForActorsReady(ctx)

	// Start could theoretically get called by multiple goroutines concurrently
	wfe.startMutex.Lock()
	defer wfe.startMutex.Unlock()

	if wfe.IsRunning {
		return nil
	}

	if wfe.actorRuntime == nil {
		return errors.New("actor runtime is not configured")
	}
	if wfe.executor == nil {
		return errors.New("gRPC executor is not yet configured")
	}

	for actorType, actor := range wfe.GetInternalActorsMap() {
		err = wfe.actorRuntime.RegisterInternalActor(ctx, actorType, actor, time.Minute*1)
		if err != nil {
			return fmt.Errorf("failed to register workflow actor %s: %w", actorType, err)
		}
	}

	// There are separate "workers" for executing orchestrations (workflows) and activities
	orchestrationWorker := backend.NewOrchestrationWorker(
		wfe.backend,
		wfe.executor,
		wfBackendLogger,
		backend.WithMaxParallelism(wfe.spec.GetMaxConcurrentWorkflowInvocations()))
	activityWorker := backend.NewActivityTaskWorker(
		wfe.backend,
		wfe.executor,
		wfBackendLogger,
		backend.WithMaxParallelism(wfe.spec.GetMaxConcurrentActivityInvocations()))
	wfe.worker = backend.NewTaskHubWorker(wfe.backend, orchestrationWorker, activityWorker, wfBackendLogger)

	// Start the Durable Task worker, which will allow workflows to be scheduled and execute.
	if err := wfe.worker.Start(ctx); err != nil {
		return fmt.Errorf("failed to start workflow engine: %w", err)
	}

	wfe.IsRunning = true
	wfLogger.Info("Workflow engine started")

	return nil
}

func (wfe *WorkflowEngine) Close(ctx context.Context) error {
	wfe.startMutex.Lock()
	defer wfe.startMutex.Unlock()

	if !wfe.IsRunning {
		return nil
	}

	if wfe.worker != nil {
		if wfe.disconnectChan != nil {
			// Signals to the durabletask-go gRPC service to disconnect the app client.
			// This is important to complete the graceful shutdown sequence in a timely manner.
			close(wfe.disconnectChan)
		}
		if err := wfe.worker.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown the workflow worker: %w", err)
		}
		wfe.IsRunning = false
		wfLogger.Info("Workflow engine stopped")
	}
	return nil
}
