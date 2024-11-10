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
	"fmt"

	"github.com/microsoft/durabletask-go/backend"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/components/wfbackend"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/runtime/processor"
	backendactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	"github.com/dapr/kit/logger"
)

var (
	log             = logger.NewLogger("dapr.runtime.wfengine")
	wfBackendLogger = logger.NewLogger("wfengine.durabletask.backend")
)

type Options struct {
	AppID          string
	Namespace      string
	Actors         actors.Interface
	Spec           config.WorkflowSpec
	BackendManager processor.WorkflowBackendManager
}

type WorkflowEngine struct {
	appID     string
	namespace string
	actors    actors.Interface

	backend        backend.Backend
	executor       backend.Executor
	worker         backend.TaskHubWorker
	backendManager processor.WorkflowBackendManager
	// TODO: @joshvanl: remove
	registerGrpcServerFn func(grpcServer grpc.ServiceRegistrar)

	initDoneCh chan struct{}
	readyCh    chan struct{}
	spec       config.WorkflowSpec
}

func New(opts Options) *WorkflowEngine {
	return &WorkflowEngine{
		appID:          opts.AppID,
		namespace:      opts.Namespace,
		spec:           opts.Spec,
		actors:         opts.Actors,
		backendManager: opts.BackendManager,
		readyCh:        make(chan struct{}),
		initDoneCh:     make(chan struct{}),
	}
}

func (wfe *WorkflowEngine) RegisterGrpcServer(grpcServer *grpc.Server) error {
	select {
	case <-wfe.initDoneCh:
	default:
		return fmt.Errorf("workflow engine not initialized")
	}
	wfe.registerGrpcServerFn(grpcServer)
	return nil
}

func (wfe *WorkflowEngine) Init() {
	defer close(wfe.initDoneCh)

	engine, ok := wfe.backendManager.Backend()
	if !ok {
		// If no backend was initialized by the manager, create a backend backed by actors
		engine = backendactors.New(wfbackend.Options{
			AppID:     wfe.appID,
			Namespace: wfe.namespace,
			Actors:    wfe.actors,
		})
	}

	wfe.backend = engine

	wfe.executor, wfe.registerGrpcServerFn = backend.NewGrpcExecutor(wfe.backend, log)
}

func (wfe *WorkflowEngine) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wfe.initDoneCh:
	}

	// Register actor backend if backend is actor
	abe, ok := wfe.backend.(*backendactors.Actors)
	if ok {
		abe.RegisterActor(ctx)
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

	log.Info("Workflow engine started")

	close(wfe.readyCh)
	<-ctx.Done()

	if err := wfe.worker.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("failed to shutdown the workflow worker: %w", err)
	}

	log.Info("Workflow engine stopped")
	return nil
}

func (wfe *WorkflowEngine) WaitForReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wfe.readyCh:
		return nil
	}
}

func IsWorkflowRequest(path string) bool {
	return backend.IsDurableTaskGrpcRequest(path)
}
