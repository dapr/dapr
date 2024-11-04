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

	"github.com/dapr/dapr/pkg/actors/engine"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/kit/logger"
)

var (
	log             = logger.NewLogger("dapr.runtime.wfengine")
	wfBackendLogger = logger.NewLogger("wfengine.durabletask.backend")
)

type Options struct {
	AppID          string
	Namespace      string
	ActorEngine    engine.Interface
	Spec           config.WorkflowSpec
	BackendManager processor.WorkflowBackendManager
}

type WorkflowEngine struct {
	isRunning atomic.Bool

	backend              backend.Backend
	executor             backend.Executor
	worker               backend.TaskHubWorker
	registerGrpcServerFn func(grpcServer grpc.ServiceRegistrar)

	startMutex      sync.Mutex
	disconnectChan  chan any
	spec            config.WorkflowSpec
	wfEngineReady   atomic.Bool
	wfEngineReadyCh chan struct{}
}

func IsWorkflowRequest(path string) bool {
	return backend.IsDurableTaskGrpcRequest(path)
}

func New(opts Options) *WorkflowEngine {
	engine := &WorkflowEngine{
		spec:            opts.Spec,
		wfEngineReadyCh: make(chan struct{}),
	}

	var ok bool
	if opts.BackendManager != nil {
		engine.backend, ok = opts.BackendManager.Backend()
	}
	if !ok {
		// If no backend was initialized by the manager, create a backend backed by actors
		// TODO: @joshvanl
		//engine.backend = actors.New(actors.Options{
		//	AppID:       opts.AppID,
		//	Namespace:   opts.Namespace,
		//	ActorEngine: opts.ActorEngine,
		//})
	}

	return engine
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

	wfe.executor, wfe.registerGrpcServerFn = backend.NewGrpcExecutor(wfe.backend, log, autoStartCallback, disconnectHelper)
}

func (wfe *WorkflowEngine) Start(ctx context.Context) (err error) {
	// Start could theoretically get called by multiple goroutines concurrently
	wfe.startMutex.Lock()
	defer wfe.startMutex.Unlock()

	if wfe.isRunning.Load() {
		return nil
	}

	if wfe.executor == nil {
		return errors.New("gRPC executor is not yet configured")
	}

	// Register actor backend if backend is actor
	// TODO: @joshvanl
	//abe, ok := wfe.backend.(*actors.Actors)
	//if ok {
	//	abe.WaitForActorsReady(ctx)
	//	abe.RegisterActor(ctx)
	//}

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

	wfe.isRunning.Store(true)
	log.Info("Workflow engine started")

	wfe.SetWorkflowEngineReadyDone()

	return nil
}

func (wfe *WorkflowEngine) Close(ctx context.Context) error {
	wfe.startMutex.Lock()
	defer wfe.startMutex.Unlock()

	if !wfe.isRunning.Load() {
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
		wfe.isRunning.Store(false)
		log.Info("Workflow engine stopped")
	}
	return nil
}

// WaitForWorkflowEngineReady waits for the workflow engine to be ready.
func (wfe *WorkflowEngine) WaitForWorkflowEngineReady(ctx context.Context) {
	// Quick check to avoid allocating a timer if the workflow engine is ready
	if wfe.wfEngineReady.Load() {
		return
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
	defer waitCancel()

	select {
	case <-waitCtx.Done():
	case <-wfe.wfEngineReadyCh:
	}
}

func (wfe *WorkflowEngine) SetWorkflowEngineReadyDone() {
	if wfe.wfEngineReady.CompareAndSwap(false, true) {
		close(wfe.wfEngineReadyCh)
	}
}
