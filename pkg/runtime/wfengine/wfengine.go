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
	"time"

	"github.com/microsoft/durabletask-go/backend"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

type WorkflowEngine struct {
	IsRunning bool

	backend              *actorBackend
	executor             backend.Executor
	worker               backend.TaskHubWorker
	registerGrpcServerFn func(grpcServer grpc.ServiceRegistrar)

	workflowActor *workflowActor
	activityActor *activityActor

	actorRuntime   actors.Actors
	startMutex     sync.Mutex
	disconnectChan chan any
	config         wfConfig
}

const (
	defaultNamespace     = "default"
	WorkflowNameLabelKey = "workflow"
	ActivityNameLabelKey = "activity"
)

var (
	wfLogger            = logger.NewLogger("dapr.runtime.wfengine")
	wfBackendLogger     = logger.NewLogger("wfengine.backend")
	errExecutionAborted = errors.New("execution aborted")
)

// wfConfig is the configuration for the workflow engine
type wfConfig struct {
	AppID             string
	workflowActorType string
	activityActorType string
}

// NewWorkflowConfig creates a new workflow engine configuration
func NewWorkflowConfig(appID string) wfConfig {
	return wfConfig{
		AppID:             appID,
		workflowActorType: actors.InternalActorTypePrefix + utils.GetNamespaceOrDefault(defaultNamespace) + utils.DotDelimiter + appID + utils.DotDelimiter + WorkflowNameLabelKey,
		activityActorType: actors.InternalActorTypePrefix + utils.GetNamespaceOrDefault(defaultNamespace) + utils.DotDelimiter + appID + utils.DotDelimiter + ActivityNameLabelKey,
	}
}

func IsWorkflowRequest(path string) bool {
	return backend.IsDurableTaskGrpcRequest(path)
}

func NewWorkflowEngine(config wfConfig) *WorkflowEngine {
	// In order to lazily start the engine (i.e. when it is invoked
	// by the application when it registers workflows / activities or by
	// an API call to interact with the engine) we need to inject the engine
	// into the backend because the backend is what is registered with the gRPC
	// service and needs to have a reference in order to start it.
	engine := &WorkflowEngine{
		config: config,
	}
	be := NewActorBackend(engine)
	engine.backend = be
	engine.activityActor = NewActivityActor(be, config)
	engine.workflowActor = NewWorkflowActor(be, config)

	return engine
}

// InternalActors returns a map of internal actors that are used to implement workflows
func (wfe *WorkflowEngine) InternalActors() map[string]actors.InternalActor {
	internalActors := make(map[string]actors.InternalActor)
	internalActors[wfe.config.workflowActorType] = wfe.workflowActor
	internalActors[wfe.config.activityActorType] = wfe.activityActor
	return internalActors
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

func (wfe *WorkflowEngine) SetActorRuntime(actorRuntime actors.Actors) error {
	wfLogger.Info("Configuring workflow engine with actors backend")
	wfe.actorRuntime = actorRuntime
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

func (wfe *WorkflowEngine) Start(ctx context.Context) (err error) {
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

	for actorType, actor := range wfe.InternalActors() {
		err = wfe.actorRuntime.RegisterInternalActor(ctx, actorType, actor)
		if err != nil {
			return fmt.Errorf("failed to register workflow actor %s: %w", actorType, err)
		}
	}

	// TODO: Determine whether a more dynamic parallelism configuration is necessary.
	parallelismOpts := backend.WithMaxParallelism(100)

	orchestrationWorker := backend.NewOrchestrationWorker(wfe.backend, wfe.executor, wfBackendLogger, parallelismOpts)
	activityWorker := backend.NewActivityTaskWorker(wfe.backend, wfe.executor, wfBackendLogger, parallelismOpts)
	wfe.worker = backend.NewTaskHubWorker(wfe.backend, orchestrationWorker, activityWorker, wfBackendLogger)
	if err := wfe.worker.Start(ctx); err != nil {
		return fmt.Errorf("failed to start workflow engine: %w", err)
	}

	wfe.IsRunning = true
	wfLogger.Info("Workflow engine started")

	return nil
}

func (wfe *WorkflowEngine) Stop(ctx context.Context) error {
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
