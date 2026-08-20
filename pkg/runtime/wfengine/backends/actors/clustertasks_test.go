/*
Copyright 2026 The Dapr Authors
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor/pending"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
)

// newClusterTasksTestBackend wires a ClusterTasksBackend to a real executor
// actor factory through fakes: the router invokes executor actors in-process
// and placement always resolves the executor actor locally, mirroring the
// co-located steady state under WorkflowsClusteredDeployment.
func newClusterTasksTestBackend(t *testing.T) *ClusterTasksBackend {
	return newClusterTasksTestBackendPlaced(t, true)
}

// newClusterTasksTestBackendPlaced is newClusterTasksTestBackend with control
// over whether placement resolves the executor actor locally; local=false
// exercises the watch-stream fallback.
func newClusterTasksTestBackendPlaced(t *testing.T, local bool) *ClusterTasksBackend {
	t.Helper()

	const executorType = "dapr.internal.default.test.executor"
	p := pending.New()

	var execFactory targets.Factory
	routerFake := routerfake.New().
		WithCallFn(func(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
			return execFactory.GetOrCreate(req.GetActor().GetActorId()).InvokeMethod(ctx, req)
		}).
		WithCallStreamFn(func(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
			return execFactory.GetOrCreate(req.GetActor().GetActorId()).InvokeStream(ctx, req, stream)
		})
	actorsFake := actorsfake.New().
		WithRouter(func(context.Context) (router.Interface, error) {
			return routerFake, nil
		}).
		WithPlacementLookupActor(func(context.Context, *actorsapi.LookupActorRequest) (*actorsapi.LookupActorResponse, error) {
			return &actorsapi.LookupActorResponse{Local: local}, nil
		})

	var err error
	execFactory, err = executor.New(t.Context(), executor.Options{
		Actors:    actorsFake,
		ActorType: executorType,
		Pending:   p,
	})
	require.NoError(t, err)

	be, err := NewClusterTasksBackend(ClusterTasksBackendOptions{
		Actors:            actorsFake,
		ExecutorActorType: executorType,
		Pending:           p,
	})
	require.NoError(t, err)
	return be
}

// Test_waitForCompletionClaimsParkedResults is the regression test for the
// register-then-claim race: the completion RPC can arrive before the waiter
// registers in the pending map, in which case it parks on the co-located
// executor actor. The wait must drain it via Claim instead of blocking
// forever on the pending map.
func Test_waitForCompletionClaimsParkedResults(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second*10)
	t.Cleanup(cancel)

	t.Run("activity completion before registration", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		wait := be.WaitForActivityCompletion(&protos.ActivityRequest{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "i1"},
			TaskId:           0,
		})

		require.NoError(t, be.CompleteActivityTask(ctx, &protos.ActivityResponse{
			InstanceId: "i1",
			TaskId:     0,
			Result:     wrapperspb.String("ok"),
		}))

		resp, err := wait(ctx)
		require.NoError(t, err)
		assert.Equal(t, "ok", resp.GetResult().GetValue())
	})

	t.Run("workflow completion before registration", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		wait := be.WaitForWorkflowTaskCompletion(&protos.WorkflowRequest{
			InstanceId: "w1",
		})

		require.NoError(t, be.CompleteWorkflowTask(ctx, &protos.WorkflowResponse{
			InstanceId: "w1",
		}))

		resp, err := wait(ctx)
		require.NoError(t, err)
		assert.Equal(t, "w1", resp.GetInstanceId())
	})

	t.Run("activity cancellation before registration", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		wait := be.WaitForActivityCompletion(&protos.ActivityRequest{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "i2"},
			TaskId:           0,
		})

		require.NoError(t, be.CancelActivityTask(ctx, "i2", 0))

		_, err := wait(ctx)
		require.ErrorIs(t, err, api.ErrTaskCancelled)
	})

	t.Run("completion after registration is delivered via the pending map", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		wait := be.WaitForActivityCompletion(&protos.ActivityRequest{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "i3"},
			TaskId:           0,
		})

		resCh := make(chan *protos.ActivityResponse, 1)
		errCh := make(chan error, 1)
		go func() {
			resp, err := wait(ctx)
			errCh <- err
			resCh <- resp
		}()

		assert.EventuallyWithT(t, func(col *assert.CollectT) {
			assert.Equal(col, 1, be.pending.Len())
		}, time.Second*5, time.Millisecond)

		require.NoError(t, be.CompleteActivityTask(ctx, &protos.ActivityResponse{
			InstanceId: "i3",
			TaskId:     0,
			Result:     wrapperspb.String("ok"),
		}))

		require.NoError(t, <-errCh)
		assert.Equal(t, "ok", (<-resCh).GetResult().GetValue())
	})
}

// Test_onCompletion covers the event-driven registration path behind
// backend.CompletionCallbackBackend: the callback runs on the goroutine that
// delivers the completion, registration is eager, and stale parked
// completions are claimed at registration time.
func Test_onCompletion(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second*10)
	t.Cleanup(cancel)

	t.Run("activity completion runs the callback on the completing goroutine", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		var resp *protos.ActivityResponse
		var cbErr error
		dereg := be.OnActivityCompletion(&protos.ActivityRequest{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "a1"},
			TaskId:           0,
		}, func(r *protos.ActivityResponse, err error) {
			resp, cbErr = r, err
		})
		t.Cleanup(dereg)

		require.NoError(t, be.CompleteActivityTask(ctx, &protos.ActivityResponse{
			InstanceId: "a1",
			TaskId:     0,
			Result:     wrapperspb.String("ok"),
		}))

		// No synchronization: the callback must have run before
		// CompleteActivityTask returned, on this goroutine.
		require.NoError(t, cbErr)
		require.NotNil(t, resp)
		assert.Equal(t, "ok", resp.GetResult().GetValue())
		assert.Equal(t, 1, be.pending.Len(), "the registration must stay armed until deregistered")
	})

	t.Run("workflow completion runs the callback on the completing goroutine", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		var resp *protos.WorkflowResponse
		var cbErr error
		dereg := be.OnWorkflowTaskCompletion(&protos.WorkflowRequest{
			InstanceId: "w1",
		}, func(r *protos.WorkflowResponse, err error) {
			resp, cbErr = r, err
		})
		t.Cleanup(dereg)

		require.NoError(t, be.CompleteWorkflowTask(ctx, &protos.WorkflowResponse{
			InstanceId: "w1",
		}))

		require.NoError(t, cbErr)
		require.NotNil(t, resp)
		assert.Equal(t, "w1", resp.GetInstanceId())
	})

	t.Run("stale parked completion is claimed at registration", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		// No waiter registered: the completion parks on the co-located
		// executor actor, as a stale completion of a superseded attempt does.
		require.NoError(t, be.CompleteActivityTask(ctx, &protos.ActivityResponse{
			InstanceId: "a2",
			TaskId:     0,
			Result:     wrapperspb.String("stale"),
		}))

		var resp *protos.ActivityResponse
		var cbErr error
		dereg := be.OnActivityCompletion(&protos.ActivityRequest{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "a2"},
			TaskId:           0,
		}, func(r *protos.ActivityResponse, err error) {
			resp, cbErr = r, err
		})
		t.Cleanup(dereg)

		require.NoError(t, cbErr)
		require.NotNil(t, resp, "registration must drain the parked completion")
		assert.Equal(t, "stale", resp.GetResult().GetValue())
		// The drained payload may be a superseded attempt's that durabletask
		// discards by token: the registration must stay armed for the
		// genuine completion, and only deregistration removes it.
		assert.Equal(t, 1, be.pending.Len(), "the registration must survive the parked-claim drain")
		require.NoError(t, be.CompleteActivityTask(ctx, &protos.ActivityResponse{
			InstanceId: "a2",
			TaskId:     0,
			Result:     wrapperspb.String("genuine"),
		}))
		assert.Equal(t, "genuine", resp.GetResult().GetValue(),
			"a delivery after the stale drain must still reach the callback")
		dereg()
		assert.Equal(t, 0, be.pending.Len())
	})

	t.Run("cancellation surfaces ErrTaskCancelled", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		var cbErr error
		fired := false
		dereg := be.OnActivityCompletion(&protos.ActivityRequest{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "a3"},
			TaskId:           0,
		}, func(_ *protos.ActivityResponse, err error) {
			fired, cbErr = true, err
		})
		t.Cleanup(dereg)

		require.NoError(t, be.CancelActivityTask(ctx, "a3", 0))
		require.True(t, fired)
		require.ErrorIs(t, cbErr, api.ErrTaskCancelled)
	})

	t.Run("parked cancellation is claimed at registration", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		require.NoError(t, be.CancelActivityTask(ctx, "a4", 0))

		var cbErr error
		fired := false
		dereg := be.OnActivityCompletion(&protos.ActivityRequest{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "a4"},
			TaskId:           0,
		}, func(_ *protos.ActivityResponse, err error) {
			fired, cbErr = true, err
		})
		t.Cleanup(dereg)

		require.True(t, fired)
		require.ErrorIs(t, cbErr, api.ErrTaskCancelled)
	})

	t.Run("deregister prevents delivery", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackend(t)

		fired := false
		dereg := be.OnActivityCompletion(&protos.ActivityRequest{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "a5"},
			TaskId:           0,
		}, func(*protos.ActivityResponse, error) {
			fired = true
		})
		dereg()

		require.NoError(t, be.CompleteActivityTask(ctx, &protos.ActivityResponse{
			InstanceId: "a5",
			TaskId:     0,
		}))
		assert.False(t, fired, "deregistered callback must never fire")
		dereg()
	})

	t.Run("non-local placement falls back to the watch stream", func(t *testing.T) {
		t.Parallel()
		be := newClusterTasksTestBackendPlaced(t, false)

		respCh := make(chan *protos.WorkflowResponse, 1)
		errCh := make(chan error, 1)
		dereg := be.OnWorkflowTaskCompletion(&protos.WorkflowRequest{
			InstanceId: "w2",
		}, func(r *protos.WorkflowResponse, err error) {
			respCh <- r
			errCh <- err
		})
		t.Cleanup(dereg)

		assert.Equal(t, 0, be.pending.Len(), "watch fallback must not hold a pending-map registration")

		require.NoError(t, be.CompleteWorkflowTask(ctx, &protos.WorkflowResponse{
			InstanceId: "w2",
		}))

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-ctx.Done():
			require.Fail(t, "timed out waiting for the watch-stream delivery")
		}
		assert.Equal(t, "w2", (<-respCh).GetInstanceId())
	})
}
