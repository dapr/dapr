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

	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
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
}

type Options struct {
	AppID              string
	Namespace          string
	Actors             actors.Interface
	Spec               config.WorkflowSpec
	BackendManager     processor.WorkflowBackendManager
	Resiliency         resiliency.Provider
	SchedulerReminders bool
}

type engine struct {
	appID     string
	namespace string
	actors    actors.Interface

	worker  backend.TaskHubWorker
	backend *backendactors.Actors
	client  workflows.Workflow

	registerGrpcServerFn func(grpcServer grpc.ServiceRegistrar)
}

func New(opts Options) (Interface, error) {
	// If no backend was initialized by the manager, create a backend backed by actors
	abackend, err := backendactors.New(backendactors.Options{
		AppID:              opts.AppID,
		Namespace:          opts.Namespace,
		Actors:             opts.Actors,
		Resiliency:         opts.Resiliency,
		SchedulerReminders: opts.SchedulerReminders,
	})
	if err != nil {
		return nil, err
	}

	var activeConns uint64
	var lock sync.Mutex
	executor, registerGrpcServerFn := backend.NewGrpcExecutor(abackend, log,
		backend.WithOnGetWorkItemsConnectionCallback(func(ctx context.Context) error {
			lock.Lock()
			defer lock.Unlock()

			activeConns++
			if activeConns == 1 {
				log.Debug("Registering workflow actors")
				return abackend.RegisterActors(ctx)
			}

			return nil
		}),
		backend.WithOnGetWorkItemsDisconnectCallback(func(ctx context.Context) error {
			lock.Lock()
			defer lock.Unlock()

			activeConns--
			if activeConns == 0 {
				log.Debug("Unregistering workflow actors")
				return abackend.UnRegisterActors(ctx)
			}

			return nil
		}),
	)

	// There are separate "workers" for executing orchestrations (workflows) and activities
	orchestrationWorker := backend.NewOrchestrationWorker(
		abackend,
		executor,
		wfBackendLogger,
		backend.WithMaxParallelism(opts.Spec.GetMaxConcurrentWorkflowInvocations()))
	activityWorker := backend.NewActivityTaskWorker(
		abackend,
		executor,
		wfBackendLogger,
		backend.WithMaxParallelism(opts.Spec.GetMaxConcurrentActivityInvocations()))
	worker := backend.NewTaskHubWorker(abackend, orchestrationWorker, activityWorker, wfBackendLogger)

	return &engine{
		appID:                opts.AppID,
		namespace:            opts.Namespace,
		actors:               opts.Actors,
		worker:               worker,
		backend:              abackend,
		registerGrpcServerFn: registerGrpcServerFn,
		client: &client{
			logger: wfBackendLogger,
			client: backend.NewTaskHubClient(abackend),
		},
	}, nil
}

func (wfe *engine) RegisterGrpcServer(server *grpc.Server) {
	wfe.registerGrpcServerFn(server)
}

func (wfe *engine) Run(ctx context.Context) error {
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
