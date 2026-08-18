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
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors"
	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor/pending"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
)

type ClusterTasksBackendOptions struct {
	Actors            actors.Interface
	ExecutorActorType string
	Pending           *pending.Pending
}

// ClusterTasksBackend rendezvouses pending work-item waiters with the
// completions reported by the application when daprd runs behind a load
// balancer (WorkflowsClusteredDeployment): the completion RPC can land on any
// daprd, not necessarily the one hosting the pending work item.
//
// The waiter always runs on the daprd hosting the workflow or activity actor,
// and it registers in the process-local pending map. The executor rendezvous
// actor shares its actor ID with the waiter's actor (the workflow instance ID
// for workflow tasks, the activity actor ID for activity tasks), so
// placement resolves it to the waiter's host: a completion arriving on
// another daprd is forwarded via a single executor actor call and delivered
// into the pending map in-process. The watch-stream path on the executor
// actor is kept as a fallback for waiters whose executor actor is not local
// (legacy-format activity reminders created by pre-upgrade daprds, placement
// disagreement windows).
type ClusterTasksBackend struct {
	actors            actors.Interface
	executorActorType string
	pending           *pending.Pending
}

func NewClusterTasksBackend(opts ClusterTasksBackendOptions) (*ClusterTasksBackend, error) {
	if opts.Pending == nil {
		// A silently defaulted map would deliver into a different instance
		// than the executor actor factory's, losing every cross-daprd
		// completion; fail fast at construction instead.
		return nil, errors.New("pending rendezvous is required and must be the same instance passed to executor.Options.Pending")
	}
	return &ClusterTasksBackend{
		actors:            opts.Actors,
		executorActorType: opts.ExecutorActorType,
		pending:           opts.Pending,
	}, nil
}

func (be *ClusterTasksBackend) CompleteActivityTask(ctx context.Context, resp *protos.ActivityResponse) error {
	key := common.ActivityActorID(
		resp.GetInstanceId(),
		resp.GetTaskId(),
	)

	data, err := proto.Marshal(resp)
	if err != nil {
		return err
	}

	return be.completeTask(ctx, executor.TaskTypeActivity, key, data)
}

func (be *ClusterTasksBackend) CancelActivityTask(ctx context.Context, id api.InstanceID, taskID int32) error {
	key := common.ActivityActorID(
		string(id),
		taskID,
	)

	return be.cancelTask(ctx, executor.TaskTypeActivity, key)
}

func (be *ClusterTasksBackend) CompleteWorkflowTask(ctx context.Context, resp *protos.WorkflowResponse) error {
	data, err := proto.Marshal(resp)
	if err != nil {
		return err
	}

	return be.completeTask(ctx, executor.TaskTypeWorkflow, resp.GetInstanceId(), data)
}

func (be *ClusterTasksBackend) CancelWorkflowTask(ctx context.Context, id api.InstanceID) error {
	return be.cancelTask(ctx, executor.TaskTypeWorkflow, string(id))
}

// completeTask delivers a completion to the waiter registered for key. When
// the waiter lives on this daprd its pending-map entry is completed
// in-process; otherwise the completion is forwarded via the executor actor,
// which placement resolves to the waiter's host. Pending entries are
// namespaced by task type: the executor actor ID space is shared between
// workflow and activity tasks and instance IDs may collide with activity
// keys (see executor.PendingKey).
func (be *ClusterTasksBackend) completeTask(ctx context.Context, taskType, key string, data []byte) error {
	if be.pending.Deliver(executor.PendingKey(taskType, key), data) {
		diag.DefaultWorkflowMonitoring.WorkflowCompletionRoute(ctx, taskType, diag.CompletionRouteCompleteLocal)
		return nil
	}

	router, err := be.actors.Router(ctx)
	if err != nil {
		return err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(executor.MethodComplete).
		WithActor(be.executorActorType, key).
		WithData(data).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{executor.MetadataTaskType: {taskType}})

	_, err = router.Call(ctx, req)
	if err == nil {
		diag.DefaultWorkflowMonitoring.WorkflowCompletionRoute(ctx, taskType, diag.CompletionRouteCompleteActor)
	}

	return err
}

func (be *ClusterTasksBackend) cancelTask(ctx context.Context, taskType, key string) error {
	if be.pending.Cancel(executor.PendingKey(taskType, key)) {
		return nil
	}

	router, err := be.actors.Router(ctx)
	if err != nil {
		return err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(executor.MethodCancel).
		WithActor(be.executorActorType, key).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{executor.MetadataTaskType: {taskType}})

	_, err = router.Call(ctx, req)

	return err
}

func (be *ClusterTasksBackend) WaitForActivityCompletion(req *protos.ActivityRequest) func(context.Context) (*protos.ActivityResponse, error) {
	key := common.ActivityActorID(
		req.GetWorkflowInstance().GetInstanceId(),
		req.GetTaskId(),
	)

	wait := be.waitForCompletion(executor.TaskTypeActivity, key)

	return func(ctx context.Context) (*protos.ActivityResponse, error) {
		var resp protos.ActivityResponse
		if err := wait(ctx, &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	}
}

func (be *ClusterTasksBackend) WaitForWorkflowTaskCompletion(req *protos.WorkflowRequest) func(context.Context) (*protos.WorkflowResponse, error) {
	wait := be.waitForCompletion(executor.TaskTypeWorkflow, req.GetInstanceId())

	return func(ctx context.Context) (*protos.WorkflowResponse, error) {
		var resp protos.WorkflowResponse
		if err := wait(ctx, &resp); err != nil {
			return nil, err
		}
		return &resp, nil
	}
}

// waitForCompletion returns a function that registers a waiter for key in
// the process-local pending map and blocks until the completion is
// delivered. Registration happens when the returned function is invoked, not
// when it is constructed: durabletask may abandon the work item between
// constructing the waiter and invoking it (context cancellation during
// dispatch), and an entry registered eagerly would leak and swallow a later
// completion for the same key. The window this opens (a completion reported
// before the returned function runs) is real: the work item is dispatched
// before the returned function runs, so a fast application round trip can
// report the completion first. Such a completion parks on the co-located
// executor actor and is drained by the claim call made right after
// registration on the wait_local path below.
//
// The pending map is only consulted by completions arriving on this daprd or
// forwarded here by the executor actor, so blocking on it is only correct
// when placement resolves the executor actor for key to this host. That is
// the steady state by construction (the executor actor ID equals the
// waiter's actor ID); when it does not hold, the waiter deregisters and falls
// back to a watch stream on the executor actor, wherever it is placed.
func (be *ClusterTasksBackend) waitForCompletion(taskType, key string) func(context.Context, proto.Message) error {
	return func(ctx context.Context, resp proto.Message) error {
		waitCh, deregister := be.pending.Register(executor.PendingKey(taskType, key))
		defer deregister()

		if be.executorLocal(ctx, key) {
			diag.DefaultWorkflowMonitoring.WorkflowCompletionRoute(ctx, taskType, diag.CompletionRouteWaitLocal)

			// The completion RPC can win the race with the Register above:
			// finding no waiter, it parks the payload on the co-located
			// executor actor, which the pending map never consults, and the
			// select below would block forever (the durable reminder retry
			// cannot redispatch, because the reminder invocation is this
			// blocked call chain). Register-then-claim pairs with the
			// executor actor's deliver-then-park so whichever order the race
			// resolves in, one side observes the other. A claim error is
			// returned rather than waited out: the work item is abandoned and
			// the durable retry converges.
			if done, err := be.claimParked(ctx, taskType, key, resp); done || err != nil {
				return err
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case res := <-waitCh:
				if res.Cancelled {
					return api.ErrTaskCancelled
				}
				return proto.Unmarshal(res.Data, resp)
			}
		}

		// A completion may already have been delivered into the local map
		// between registration and this fallback decision; deregister
		// (delivery and deregistration are serialized, so nothing can be
		// delivered afterwards) and drain before watching.
		deregister()
		select {
		case res := <-waitCh:
			if res.Cancelled {
				return api.ErrTaskCancelled
			}
			return proto.Unmarshal(res.Data, resp)
		default:
		}

		diag.DefaultWorkflowMonitoring.WorkflowCompletionRoute(ctx, taskType, diag.CompletionRouteWaitWatch)

		return be.watchCompletion(ctx, taskType, key, resp)
	}
}

// claimParked drains a completion or cancellation for key that was parked on
// the co-located executor actor before the waiter registered in the pending
// map. It reports whether the wait is settled: a claimed completion is
// unmarshalled into resp, a parked cancellation surfaces as ErrTaskCancelled.
// Not-found (the steady state) leaves the waiter on its pending-map channel.
func (be *ClusterTasksBackend) claimParked(ctx context.Context, taskType, key string, resp proto.Message) (bool, error) {
	router, err := be.actors.Router(ctx)
	if err != nil {
		return false, err
	}

	req := internalsv1pb.
		NewInternalInvokeRequest(executor.MethodClaim).
		WithActor(be.executorActorType, key).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{executor.MetadataTaskType: {taskType}})

	res, err := router.Call(ctx, req)
	if err != nil {
		return false, err
	}

	switch res.GetStatus().GetCode() {
	case int32(codes.OK):
		return true, proto.Unmarshal(res.GetMessage().GetData().GetValue(), resp)
	case int32(codes.Aborted):
		return true, api.ErrTaskCancelled
	case int32(codes.NotFound):
		return false, nil
	default:
		// An unexpected status must settle the wait rather than leave the
		// waiter parked on the pending map: the work item is abandoned and
		// the durable retry converges.
		return true, fmt.Errorf("unexpected claim status %d for task %q", res.GetStatus().GetCode(), key)
	}
}

// executorLocal reports whether placement resolves the executor actor for
// key to this daprd.
func (be *ClusterTasksBackend) executorLocal(ctx context.Context, key string) bool {
	placement, err := be.actors.Placement(ctx)
	if err != nil {
		return false
	}

	lar, _, cancel, err := placement.LookupActor(ctx, &actorsapi.LookupActorRequest{
		ActorType: be.executorActorType,
		ActorID:   key,
	})
	if cancel != nil {
		cancel(nil)
	}

	return err == nil && lar.Local
}

// watchCompletion is the fallback rendezvous: a watch stream on the executor
// actor, wherever placement resolves it. The executor actor ID space is
// shared between task types, so a completion of the other type sharing this
// actor (a workflow instance ID colliding with an activity actor ID) could
// be streamed here; completions parked by current daprds carry their task
// type as a response header and are rejected on mismatch (the durable
// reminder retry converges), while completions parked by pre-upgrade daprds
// carry no header and are accepted as before.
func (be *ClusterTasksBackend) watchCompletion(ctx context.Context, taskType, key string, resp proto.Message) error {
	router, err := be.actors.Router(ctx)
	if err != nil {
		return err
	}

	// Advertising the task type lets the executor actor hand a displaced
	// completion only to a watcher of its own type; the stream-side check
	// below stays as the guard for untyped deliveries.
	sreq := internalsv1pb.
		NewInternalInvokeRequest(executor.MethodWatchComplete).
		WithActor(be.executorActorType, key).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{executor.MetadataTaskType: {taskType}})

	return router.CallStream(ctx, sreq, func(res *internalsv1pb.InternalInvokeResponse) (bool, error) {
		if res == nil {
			return false, errors.New("received nil response from task completion")
		}

		if res.GetStatus().GetCode() == int32(codes.Aborted) {
			return false, api.ErrTaskCancelled
		}

		if v, ok := res.GetHeaders()[executor.MetadataTaskType]; ok && len(v.GetValues()) > 0 && v.GetValues()[0] != taskType {
			return false, fmt.Errorf("received completion for task type %q while watching %q", v.GetValues()[0], taskType)
		}

		if err := proto.Unmarshal(res.GetMessage().GetData().GetValue(), resp); err != nil {
			return false, err
		}

		return true, nil
	})
}
