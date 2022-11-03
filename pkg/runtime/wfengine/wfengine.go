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

	WorkflowActor actors.InternalActor
	ActivityActor actors.InternalActor
}

var wfLogger = logger.NewLogger("dapr.runtime.wfengine")

func IsWorkflowRequest(path string) bool {
	return backend.IsDurableTaskGrpcRequest(path)
}

func NewWorkflowEngine() *WorkflowEngine {
	be := NewActorBackend()
	engine := &WorkflowEngine{
		backend:       be,
		WorkflowActor: NewWorkflowActor(be),
		ActivityActor: NewActivityActor(be),
	}
	return engine
}

// InternalActors returns a map of internal actors that are used to implement workflows
func (wfe *WorkflowEngine) InternalActors() map[string]actors.InternalActor {
	internalActors := make(map[string]actors.InternalActor)
	internalActors[WorkflowActorType] = wfe.WorkflowActor
	internalActors[ActivityActorType] = wfe.ActivityActor
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

func (wfe *WorkflowEngine) SetActorRuntime(actorRuntime actors.Actors) {
	wfLogger.Info("configuring workflow engine with actors backend")
	wfe.backend.SetActorRuntime(actorRuntime)
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
