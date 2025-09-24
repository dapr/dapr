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

type ClusterTasksBackend struct {
	actors            actors.Interface
	executorActorType string
}

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

func (be *ClusterTasksBackend) WaitForActivityCompletion(ctx context.Context, req *protos.ActivityRequest) (*protos.ActivityResponse, error) {
	router, err := be.actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	key := backend.GetActivityExecutionKey(
		req.GetOrchestrationInstance().GetInstanceId(),
		req.GetTaskId(),
	)
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
		if err = proto.Unmarshal(res.GetMessage().GetData().GetValue(), &resp); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}

	return &resp, nil
}

func (be *ClusterTasksBackend) CompleteOrchestratorTask(ctx context.Context, resp *protos.OrchestratorResponse) error {
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

func (be *ClusterTasksBackend) CancelOrchestratorTask(ctx context.Context, id api.InstanceID) error {
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

func (be *ClusterTasksBackend) WaitForOrchestratorCompletion(ctx context.Context, req *protos.OrchestratorRequest) (*protos.OrchestratorResponse, error) {
	router, err := be.actors.Router(ctx)
	if err != nil {
		return nil, err
	}

	sreq := internalsv1pb.
		NewInternalInvokeRequest(executor.MethodWatchComplete).
		WithActor(be.executorActorType, req.GetInstanceId()).
		WithContentType(invokev1.ProtobufContentType)

	var resp protos.OrchestratorResponse
	err = router.CallStream(ctx, sreq, func(res *internalsv1pb.InternalInvokeResponse) (bool, error) {
		if res == nil {
			return false, errors.New("received nil response from activity completion")
		}
		if res.GetStatus().GetCode() == int32(codes.Aborted) {
			return false, api.ErrTaskCancelled
		}
		if err = proto.Unmarshal(res.GetMessage().GetData().GetValue(), &resp); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}

	return &resp, nil
}
