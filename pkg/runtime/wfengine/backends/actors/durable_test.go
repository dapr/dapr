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
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor/pending"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors/pendingtracker"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/local"
)

func newDurableBackend() *Actors {
	return &Actors{
		pendingTasksBackend: pendingtracker.New(local.NewTasksBackend()),
		durables:            todo.NewDurables(),
	}
}

type testTurn struct {
	durable  *todo.Durable
	callback chan bool
}

// engineStep is what a stand-in for the engine does with a result it
// takes.
type engineStep int

const (
	completeTurn engineStep = iota
	abandonTurn
	// continueTurn stands for a continue-as-new step: the engine runs the
	// workflow again and the work item stays open.
	continueTurn
)

// startTurn hands a workflow work item, dispatched with completion token
// want, to a stand-in for the engine. Like durabletask, it ignores a result
// whose completion token is neither want nor empty, records want on the work
// item, and ends the work item on the delivering goroutine.
func startTurn(t *testing.T, abe *Actors, instanceID, want string, step engineStep) *testTurn {
	t.Helper()

	turn := &testTurn{durable: todo.NewDurable(), callback: make(chan bool, 1)}
	wi := &backend.WorkflowWorkItem{
		InstanceID: api.InstanceID(instanceID),
		Properties: map[string]any{
			todo.CallbackChannelProperty: turn.callback,
			todo.DurableProperty:         turn.durable,
		},
	}

	var (
		once  sync.Once
		lock  sync.Mutex
		dereg func()
	)
	lock.Lock()
	dereg = abe.OnWorkflowTaskCompletion(&protos.WorkflowRequest{InstanceId: instanceID}, func(resp *protos.WorkflowResponse, _ error) {
		if token := resp.GetCompletionToken(); token != "" && token != want {
			return
		}
		once.Do(func() {
			lock.Lock()
			dereg()
			lock.Unlock()
			wi.CompletionToken = want
			switch step {
			case completeTurn:
				assert.NoError(t, abe.CompleteWorkflowWorkItem(t.Context(), wi))
			case abandonTurn:
				assert.NoError(t, abe.AbandonWorkflowWorkItem(t.Context(), wi))
			case continueTurn:
			}
		})
	})
	lock.Unlock()
	return turn
}

func completeAsync(abe *Actors, ctx context.Context, resp *protos.WorkflowResponse) <-chan error {
	errCh := make(chan error, 1)
	go func() { errCh <- abe.CompleteWorkflowTask(ctx, resp) }()
	return errCh
}

func requireAnswer(t *testing.T, errCh <-chan error) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the completion call was not answered")
		return nil
	}
}

func requireNoAnswer(t *testing.T, errCh <-chan error) {
	t.Helper()
	select {
	case err := <-errCh:
		require.FailNow(t, "the completion call was answered before the turn was saved", "err: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}

func requireTaken(t *testing.T, turn *testTurn) {
	t.Helper()
	select {
	case completed := <-turn.callback:
		assert.True(t, completed)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the result did not reach the workflow actor")
	}
}

func TestCompleteWorkflowTaskWaitsForSave(t *testing.T) {
	t.Parallel()

	t.Run("answers only after the turn is saved", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1", "t1", completeTurn)

		errCh := completeAsync(abe, t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		requireTaken(t, turn)
		requireNoAnswer(t, errCh)

		turn.durable.Settle(nil)
		require.NoError(t, requireAnswer(t, errCh))
	})

	t.Run("a result without a completion token waits for the save", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1", "t1", completeTurn)

		errCh := completeAsync(abe, t.Context(), &protos.WorkflowResponse{InstanceId: "wf1"})
		requireTaken(t, turn)
		requireNoAnswer(t, errCh)

		turn.durable.Settle(nil)
		require.NoError(t, requireAnswer(t, errCh))
	})

	t.Run("a failed save answers Aborted", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1", "t1", completeTurn)

		errCh := completeAsync(abe, t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		requireTaken(t, turn)
		requireNoAnswer(t, errCh)

		turn.durable.Settle(errors.New("state store unavailable"))
		assert.Equal(t, codes.Aborted, status.Code(requireAnswer(t, errCh)))
	})

	t.Run("a result whose processing abandons the turn answers Aborted", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1", "t1", abandonTurn)

		errCh := completeAsync(abe, t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		select {
		case completed := <-turn.callback:
			assert.False(t, completed)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "the abandon did not reach the workflow actor")
		}
		requireNoAnswer(t, errCh)

		turn.durable.Settle(errors.New("execution aborted"))
		assert.Equal(t, codes.Aborted, status.Code(requireAnswer(t, errCh)))
	})

	// The actor settles the turn with its error when its context ends while
	// the worker is still running. A result taken after that was not saved.
	t.Run("a result taken after the turn ended unsaved answers Aborted", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1", "t1", completeTurn)
		turn.durable.Settle(context.Canceled)

		err := abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		assert.Equal(t, codes.Aborted, status.Code(err))
	})

	t.Run("a cancelled call stops waiting", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1", "t1", completeTurn)

		ctx, cancel := context.WithCancel(t.Context())
		errCh := completeAsync(abe, ctx, &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		requireTaken(t, turn)
		requireNoAnswer(t, errCh)

		cancel()
		assert.Equal(t, codes.Canceled, status.Code(requireAnswer(t, errCh)))
	})

	t.Run("a stale result is answered at once", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1", "t2", completeTurn)

		require.NoError(t, abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"}))
		select {
		case <-turn.callback:
			require.FailNow(t, "the stale result must not end the work item")
		default:
		}
	})

	t.Run("a continue-as-new step is answered at once", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		startTurn(t, abe, "wf1", "t1", continueTurn)

		require.NoError(t, abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"}))
	})

	t.Run("a result for no pending task answers success", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()

		require.NoError(t, abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"}))
		require.NoError(t, abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1"}))
	})
}

func TestWaitDurable(t *testing.T) {
	t.Parallel()

	t.Run("a save that settled as the call ended is reported as saved", func(t *testing.T) {
		t.Parallel()
		durable := todo.NewDurable()
		durable.Settle(nil)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		require.NoError(t, waitDurable(ctx, durable))
	})

	t.Run("an unsaved result answers Aborted", func(t *testing.T) {
		t.Parallel()
		durable := todo.NewDurable()
		durable.Settle(errors.New("state store unavailable"))

		assert.Equal(t, codes.Aborted, status.Code(waitDurable(t.Context(), durable)))
	})
}

// TestCompleteWorkflowTaskForwarded covers a completion that lands on a
// daprd which does not own the workflow, under clustered deployment. The
// daprd that owns it delivers the result through its executor actor and
// answers once the turn is saved.
func TestCompleteWorkflowTaskForwarded(t *testing.T) {
	t.Parallel()

	// newForwarded returns a front door daprd whose completions are
	// forwarded to an owner daprd, and the owner's Durable for wf1's turn.
	// Owner and front door have their own pending map and Durables.
	newForwarded := func(t *testing.T, ownerDurables *todo.Durables) (*Actors, *todo.Durable) {
		t.Helper()
		const executorType = "dapr.internal.default.test.executor"

		ownerPending := pending.New()
		owner, err := executor.New(t.Context(), executor.Options{
			Actors:    actorsfake.New(),
			ActorType: executorType,
			Pending:   ownerPending,
			Durables:  ownerDurables,
		})
		require.NoError(t, err)

		// The owner's engine takes the result and completes the work item,
		// which was dispatched with token t1.
		turn := todo.NewDurable()
		dereg := ownerPending.RegisterCallback(executor.PendingKey(executor.TaskTypeWorkflow, "wf1"), func(res pending.Result) {
			var resp protos.WorkflowResponse
			if uerr := proto.Unmarshal(res.Data, &resp); uerr != nil {
				t.Errorf("unmarshal the forwarded result: %v", uerr)
				return
			}
			if ownerDurables != nil {
				ownerDurables.Fulfil("wf1", "t1", turn)
			}
		})
		t.Cleanup(dereg)

		routerFake := routerfake.New().
			WithCallFn(func(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
				return owner.GetOrCreate(req.GetActor().GetActorId()).InvokeMethod(ctx, req)
			})
		frontDurables := todo.NewDurables()
		front, err := NewClusterTasksBackend(ClusterTasksBackendOptions{
			Actors: actorsfake.New().WithRouter(func(context.Context) (router.Interface, error) {
				return routerFake, nil
			}).WithPlacementLookupActor(func(context.Context, *actorsapi.LookupActorRequest) (*actorsapi.LookupActorResponse, error) {
				return &actorsapi.LookupActorResponse{Local: false}, nil
			}),
			ExecutorActorType: executorType,
			Pending:           pending.New(),
			Durables:          frontDurables,
		})
		require.NoError(t, err)

		return &Actors{
			pendingTasksBackend: pendingtracker.New(front),
			durables:            frontDurables,
		}, turn
	}

	for _, token := range []string{"t1", ""} {
		t.Run("answers once the owner saved the turn, token "+token, func(t *testing.T) {
			t.Parallel()
			abe, turn := newForwarded(t, todo.NewDurables())

			errCh := completeAsync(abe, t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: token})
			requireNoAnswer(t, errCh)

			turn.Settle(nil)
			require.NoError(t, requireAnswer(t, errCh))
		})
	}

	t.Run("answers Aborted when the owner did not save the turn", func(t *testing.T) {
		t.Parallel()
		abe, turn := newForwarded(t, todo.NewDurables())

		errCh := completeAsync(abe, t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		requireNoAnswer(t, errCh)

		turn.Settle(errors.New("state store unavailable"))
		assert.Equal(t, codes.Aborted, status.Code(requireAnswer(t, errCh)))
	})

	t.Run("an owner that does not wait for saves is answered as before", func(t *testing.T) {
		t.Parallel()
		abe, _ := newForwarded(t, nil)

		require.NoError(t, abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"}))
	})
}
