/*
Copyright 2025 The Dapr Authors
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
package actors

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

type ClusterTasksBackendOptions struct {
	Actors            actors.Interface
	ExecutorActorType string
}

// ClusterTasksBackend rendezvouses work-item waiters with completions that
// may arrive on any daprd (WorkflowsClusteredDeployment), through the executor
// actor keyed by the work item. The waiter registers on that actor before the
// engine dispatches the work item (see executor.MethodRegister).
type ClusterTasksBackend struct {
	actors            actors.Interface
	executorActorType string
}

// registerTimeout bounds the registration call, which has no caller context.
const registerTimeout = 30 * time.Second

func NewClusterTasksBackend(opts ClusterTasksBackendOptions) *ClusterTasksBackend {
	return &ClusterTasksBackend{
		actors:            opts.Actors,
		executorActorType: opts.ExecutorActorType,
	}
}

func (be *ClusterTasksBackend) CompleteActivityTask(ctx context.Context, resp *protos.ActivityResponse) error {
	router, err := be.actors.Router(ctx)
	if err != nil {
		return err
	}

	key := backend.GetActivityExecutionKey(
		resp.GetInstanceId(),
		resp.GetTaskId(),
	)

	data, err := proto.Marshal(resp)
	if err != nil {
		return err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(executor.MethodComplete).
		WithActor(be.executorActorType, key).
		WithData(data).
		WithContentType(invokev1.ProtobufContentType)

	_, err = router.Call(ctx, req)

	return err
}

func (be *ClusterTasksBackend) CancelActivityTask(ctx context.Context, id api.InstanceID, taskID int32) error {
	router, err := be.actors.Router(ctx)
	if err != nil {
		return err
	}

	key := backend.GetActivityExecutionKey(
		string(id),
		taskID,
	)

	req := internalsv1pb.
		NewInternalInvokeRequest(executor.MethodCancel).
		WithActor(be.executorActorType, key).
		WithContentType(invokev1.ProtobufContentType)

	_, err = router.Call(ctx, req)

	return err
}

func (be *ClusterTasksBackend) WaitForActivityCompletion(req *protos.ActivityRequest) func(context.Context) (*protos.ActivityResponse, error) {
	key := backend.GetActivityExecutionKey(
		req.GetWorkflowInstance().GetInstanceId(),
		req.GetTaskId(),
	)

	// Called by the engine before the work item is dispatched.
	be.register(key)

	return func(ctx context.Context) (*protos.ActivityResponse, error) {
		router, err := be.actors.Router(ctx)
		if err != nil {
			return nil, err
		}

		sreq := internalsv1pb.
			NewInternalInvokeRequest(executor.MethodWatchComplete).
			WithActor(be.executorActorType, key).
			WithContentType(invokev1.ProtobufContentType)

		var resp protos.ActivityResponse

		err = router.CallStream(ctx, sreq, func(res *internalsv1pb.InternalInvokeResponse) (bool, error) {
			if res == nil {
				return false, errors.New("received nil response from activity completion")
			}

			if res.GetStatus().GetCode() == int32(codes.Aborted) {
				return false, api.ErrTaskCancelled
			}

			err = proto.Unmarshal(res.GetMessage().GetData().GetValue(), &resp)
			if err != nil {
				return false, err
			}

			return true, nil
		})
		if err != nil {
			return nil, err
		}

		return &resp, nil
	}
}

func (be *ClusterTasksBackend) CompleteWorkflowTask(ctx context.Context, resp *protos.WorkflowResponse) error {
	router, err := be.actors.Router(ctx)
	if err != nil {
		return err
	}

	data, err := proto.Marshal(resp)
	if err != nil {
		return err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(executor.MethodComplete).
		WithActor(be.executorActorType, resp.GetInstanceId()).
		WithData(data).
		WithContentType(invokev1.ProtobufContentType)

	_, err = router.Call(ctx, req)

	return err
}

func (be *ClusterTasksBackend) CancelWorkflowTask(ctx context.Context, id api.InstanceID) error {
	router, err := be.actors.Router(ctx)
	if err != nil {
		return err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(executor.MethodCancel).
		WithActor(be.executorActorType, string(id)).
		WithContentType(invokev1.ProtobufContentType)

	_, err = router.Call(ctx, req)

	return err
}

func (be *ClusterTasksBackend) WaitForWorkflowTaskCompletion(req *protos.WorkflowRequest) func(context.Context) (*protos.WorkflowResponse, error) {
	// Called by the engine before the work item is dispatched.
	be.register(req.GetInstanceId())

	return func(ctx context.Context) (*protos.WorkflowResponse, error) {
		router, err := be.actors.Router(ctx)
		if err != nil {
			return nil, err
		}

		sreq := internalsv1pb.
			NewInternalInvokeRequest(executor.MethodWatchComplete).
			WithActor(be.executorActorType, req.GetInstanceId()).
			WithContentType(invokev1.ProtobufContentType)

		var resp protos.WorkflowResponse

		err = router.CallStream(ctx, sreq, func(res *internalsv1pb.InternalInvokeResponse) (bool, error) {
			if res == nil {
				return false, errors.New("received nil response from activity completion")
			}

			if res.GetStatus().GetCode() == int32(codes.Aborted) {
				return false, api.ErrTaskCancelled
			}

			err = proto.Unmarshal(res.GetMessage().GetData().GetValue(), &resp)
			if err != nil {
				return false, err
			}

			return true, nil
		})
		if err != nil {
			return nil, err
		}

		return &resp, nil
	}
}

// register arms the executor actor for key so the next completion is held for
// this waiter and a stale one is discarded rather than delivered to it. Best
// effort: on failure (including an executor hosted by a daprd that predates
// Register) the watch stream alone rendezvouses, as before.
func (be *ClusterTasksBackend) register(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), registerTimeout)
	defer cancel()

	router, err := be.actors.Router(ctx)
	if err == nil {
		req := internalsv1pb.
			NewInternalInvokeRequest(executor.MethodRegister).
			WithActor(be.executorActorType, key).
			WithContentType(invokev1.ProtobufContentType)
		_, err = router.Call(ctx, req)
	}
	if err != nil {
		log.Warnf("Failed to register completion waiter for '%s', relying on the watch stream alone: %v", key, err)
	}
}
