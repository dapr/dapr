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

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/kit/logger"
	"github.com/microsoft/durabletask-go/backend"
	"google.golang.org/grpc"
)

type WorkflowEngine struct {
	backend       *actorBackend
	executor      backend.Executor
	workflowActor actors.InternalActor
}

var wfLogger = logger.NewLogger("dapr.runtime.wfengine")

func IsWorkflowRequest(path string) bool {
	return backend.IsDurableTaskGrpcRequest(path)
}

func NewWorkflowEngine() *WorkflowEngine {
	engine := &WorkflowEngine{
		backend: NewActorBackend(),
	}
	return engine
}

func (wfe *WorkflowEngine) ConfigureGrpc(grpcServer *grpc.Server) {
	wfLogger.Info("configuring workflow engine gRPC endpoint")
	wfe.executor = backend.NewGrpcExecutor(grpcServer, wfe.backend, wfLogger)
}

func (wfe *WorkflowEngine) ConfigureActors(actorRuntime actors.Actors) {
	wfLogger.Info("configuring workflow engine with actors backend")
	wfe.backend.SetActorRuntime(actorRuntime)
}

func (wfe *WorkflowEngine) Start(ctx context.Context) error {
	if wfe.backend == nil {
		return errors.New("backend is not yet configured")
	} else if wfe.executor == nil {
		return errors.New("grpc executor is not yet configured")
	}

	// TODO: Enable concurrency for orchestrations (workflows) and activities.
	orchestrationWorker := backend.NewOrchestrationWorker(wfe.backend, wfe.executor, wfLogger, backend.NewWorkerOptions())
	activityWorker := backend.NewActivityTaskWorker(wfe.backend, wfe.executor, wfLogger, backend.NewWorkerOptions())
	taskHubWorker := backend.NewTaskHubWorker(wfe.backend, orchestrationWorker, activityWorker, wfLogger)
	if err := taskHubWorker.Start(ctx); err != nil {
		return fmt.Errorf("failed to start workflow engine: %w", err)
	}

	wfLogger.Info("workflow engine started")
	return nil
}

// WorkflowActors returns a set of internal actors used to power the embedded Dapr Workflow engine
func (wfe *WorkflowEngine) Actors() map[string]func(actors.Actors) actors.InternalActor {
	internalActors := make(map[string]func(actors.Actors) actors.InternalActor)
	internalActors[WorkflowActorType] = func(actorRuntime actors.Actors) actors.InternalActor {
		if wfe.workflowActor == nil {
			wfe.workflowActor = NewWorkflowActor(actorRuntime, wfe.backend)
		}
		return wfe.workflowActor
	}
	// TODO: Add an entry for the activity actor
	return internalActors
}
