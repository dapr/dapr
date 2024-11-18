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
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/processor"
	backendactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	"github.com/dapr/kit/logger"
)

var (
	log             = logger.NewLogger("dapr.runtime.wfengine")
	wfBackendLogger = logger.NewLogger("dapr.wfengine.durabletask.backend")
)

type Interface interface {
	Run(context.Context) error
	Init() error
	RegisterGrpcServer(*grpc.Server) error
	WaitForReady(context.Context) error
}

type Options struct {
	AppID          string
	Namespace      string
	Actors         actors.Interface
	Spec           config.WorkflowSpec
	BackendManager processor.WorkflowBackendManager
	Resiliency     resiliency.Provider
}

type engine struct {
	appID     string
	namespace string
	actors    actors.Interface

	worker         backend.TaskHubWorker
	backend        backend.Backend
	backendManager processor.WorkflowBackendManager
	resiliency     resiliency.Provider
	// TODO: @joshvanl: remove
	registerGrpcServerFn func(grpcServer grpc.ServiceRegistrar)

	initDoneCh chan struct{}
	readyCh    chan struct{}
	spec       config.WorkflowSpec
}

func New(opts Options) Interface {
	return &engine{
		appID:          opts.AppID,
		namespace:      opts.Namespace,
		spec:           opts.Spec,
		actors:         opts.Actors,
		backendManager: opts.BackendManager,
		resiliency:     opts.Resiliency,
		readyCh:        make(chan struct{}),
		initDoneCh:     make(chan struct{}),
	}
}

func (wfe *engine) RegisterGrpcServer(grpcServer *grpc.Server) error {
	select {
	case <-wfe.initDoneCh:
	default:
		log.Error("workflow engine not initialized")
		return nil
	}
	wfe.registerGrpcServerFn(grpcServer)
	return nil
}

func (wfe *engine) Init() error {
	defer close(wfe.initDoneCh)

	engine, ok := wfe.backendManager.Backend()
	if !ok {
		// If no backend was initialized by the manager, create a backend backed by actors
		actorEngine, err := backendactors.New(wfbackend.Options{
			AppID:      wfe.appID,
			Namespace:  wfe.namespace,
			Actors:     wfe.actors,
			Resiliency: wfe.resiliency,
		})
		if err != nil {
			return err
		}
		engine = actorEngine
	}

	executor, registerGrpcServerFn := backend.NewGrpcExecutor(engine, log)
	wfe.registerGrpcServerFn = registerGrpcServerFn

	// There are separate "workers" for executing orchestrations (workflows) and activities
	orchestrationWorker := backend.NewOrchestrationWorker(
		engine,
		executor,
		wfBackendLogger,
		backend.WithMaxParallelism(wfe.spec.GetMaxConcurrentWorkflowInvocations()))
	activityWorker := backend.NewActivityTaskWorker(
		engine,
		executor,
		wfBackendLogger,
		backend.WithMaxParallelism(wfe.spec.GetMaxConcurrentActivityInvocations()))
	wfe.worker = backend.NewTaskHubWorker(engine, orchestrationWorker, activityWorker, wfBackendLogger)
	wfe.backend = engine

	return nil
}

func (wfe *engine) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wfe.initDoneCh:
	}

	if actorEngine, ok := wfe.backend.(*backendactors.Actors); ok {
		if err := actorEngine.RegisterActors(ctx); err != nil {
			log.Errorf("Failed to register workflow actors, workflows disabled: %s", err)
			<-ctx.Done()
			return nil
		}
	}

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

func (wfe *engine) WaitForReady(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wfe.readyCh:
		return nil
	}
}
