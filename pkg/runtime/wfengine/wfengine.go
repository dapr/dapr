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
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator"
	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/processor"
	backendactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

var (
	log             = logger.NewLogger("dapr.runtime.wfengine")
	wfBackendLogger = logger.NewLogger("dapr.wfengine.durabletask.backend")
)

type Interface interface {
	Run(context.Context) error
	RegisterGrpcServer(*grpc.Server)
	Client() workflows.Workflow
	RuntimeMetadata() *runtimev1pb.MetadataWorkflows

	ActivityActorType() string
}

type Options struct {
	AppID                     string
	Namespace                 string
	Actors                    actors.Interface
	Spec                      *config.WorkflowSpec
	BackendManager            processor.WorkflowBackendManager
	Resiliency                resiliency.Provider
	SchedulerReminders        bool
	EventSink                 orchestrator.EventSink
	EnableClusteredDeployment bool
}

type engine struct {
	appID             string
	namespace         string
	actors            actors.Interface
	getWorkItemsCount *atomic.Int32

	worker  backend.TaskHubWorker
	backend *backendactors.Actors
	client  workflows.Workflow

	registerGrpcServerFn func(grpcServer grpc.ServiceRegistrar)
}

func New(opts Options) Interface {
	// If no backend was initialized by the manager, create a backend backed by actors
	abackend := backendactors.New(backendactors.Options{
		AppID:                     opts.AppID,
		Namespace:                 opts.Namespace,
		Actors:                    opts.Actors,
		Resiliency:                opts.Resiliency,
		SchedulerReminders:        opts.SchedulerReminders,
		EventSink:                 opts.EventSink,
		EnableClusteredDeployment: opts.EnableClusteredDeployment,
	})

	var getWorkItemsCount atomic.Int32
	var lock sync.Mutex
	executor, registerGrpcServerFn := backend.NewGrpcExecutor(abackend, log,
		backend.WithOnGetWorkItemsConnectionCallback(func(ctx context.Context) error {
			lock.Lock()
			defer lock.Unlock()

			if getWorkItemsCount.Add(1) == 1 {
				log.Debug("Registering workflow actors")
				return abackend.RegisterActors(ctx)
			}

			return nil
		}),
		backend.WithOnGetWorkItemsDisconnectCallback(func(ctx context.Context) error {
			lock.Lock()
			defer lock.Unlock()

			if ctx.Err() != nil {
				ctx = context.Background()
			}

			if getWorkItemsCount.Add(-1) == 0 {
				log.Debug("Unregistering workflow actors")
				return abackend.UnRegisterActors(ctx)
			}

			return nil
		}),
		backend.WithStreamSendTimeout(time.Second*10),
	)

	var topts []backend.NewTaskWorkerOptions
	if opts.Spec.GetMaxConcurrentWorkflowInvocations() != nil {
		topts = []backend.NewTaskWorkerOptions{
			backend.WithMaxParallelism(*opts.Spec.GetMaxConcurrentWorkflowInvocations()),
		}
	}

	// There are separate "workers" for executing orchestrations (workflows) and activities
	oworker := backend.NewOrchestrationWorker(
		abackend,
		executor,
		wfBackendLogger,
		topts...,
	)

	topts = nil
	if opts.Spec.GetMaxConcurrentActivityInvocations() != nil {
		topts = []backend.NewTaskWorkerOptions{
			backend.WithMaxParallelism(*opts.Spec.GetMaxConcurrentActivityInvocations()),
		}
	}
	aworker := backend.NewActivityTaskWorker(
		abackend,
		executor,
		wfBackendLogger,
		topts...,
	)
	worker := backend.NewTaskHubWorker(abackend, oworker, aworker, wfBackendLogger)

	return &engine{
		appID:                opts.AppID,
		namespace:            opts.Namespace,
		actors:               opts.Actors,
		worker:               worker,
		backend:              abackend,
		registerGrpcServerFn: registerGrpcServerFn,
		getWorkItemsCount:    &getWorkItemsCount,
		client: &client{
			logger: wfBackendLogger,
			client: backend.NewTaskHubClient(abackend),
		},
	}
}

func (wfe *engine) RegisterGrpcServer(server *grpc.Server) {
	wfe.registerGrpcServerFn(server)
}

func (wfe *engine) Run(ctx context.Context) error {
	_, err := wfe.actors.Router(ctx)
	if err != nil {
		<-ctx.Done()
		return ctx.Err()
	}

	// Start the Durable Task worker, which will allow workflows to be scheduled and execute.
	if err := wfe.worker.Start(ctx); err != nil {
		return fmt.Errorf("failed to start workflow engine: %w", err)
	}

	log.Info("Workflow engine started")
	<-ctx.Done()

	if err := wfe.worker.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("failed to shutdown the workflow worker: %w", err)
	}

	log.Info("Workflow engine stopped")
	return nil
}

func (wfe *engine) Client() workflows.Workflow {
	return wfe.client
}

func (wfe *engine) ActivityActorType() string {
	return wfe.backend.ActivityActorType()
}

func (wfe *engine) RuntimeMetadata() *runtimev1pb.MetadataWorkflows {
	return &runtimev1pb.MetadataWorkflows{
		ConnectedWorkers: wfe.getWorkItemsCount.Load(),
	}
}
