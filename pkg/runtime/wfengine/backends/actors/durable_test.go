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
	clocktesting "k8s.io/utils/clock/testing"

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
		durables:            newDurables(clocktesting.NewFakeClock(time.Now())),
	}
}

type testTurn struct {
	durable  *todo.Durable
	callback chan bool
}

// startTurn hands a workflow work item to a stand-in for the engine: the
// first completion delivered for the instance settles its registration and
// completes the work item on the delivering goroutine, as durabletask does.
func startTurn(t *testing.T, abe *Actors, instanceID string) *testTurn {
	t.Helper()

	turn := &testTurn{durable: todo.NewDurable(), callback: make(chan bool, 1)}
	wi := &backend.WorkflowWorkItem{
		InstanceID: api.InstanceID(instanceID),
		Properties: map[string]any{
			todo.CallbackChannelProperty: turn.callback,
			todo.DurableProperty:         turn.durable,
		},
	}
	abe.durables.track(instanceID, turn.durable)

	var (
		once  sync.Once
		lock  sync.Mutex
		dereg func()
	)
	lock.Lock()
	dereg = abe.OnWorkflowTaskCompletion(&protos.WorkflowRequest{InstanceId: instanceID}, func(*protos.WorkflowResponse, error) {
		once.Do(func() {
			lock.Lock()
			dereg()
			lock.Unlock()
			assert.NoError(t, abe.CompleteWorkflowWorkItem(t.Context(), wi))
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

func TestCompleteWorkflowTaskWaitsForSave(t *testing.T) {
	t.Parallel()

	t.Run("answers only after the turn is saved", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1")

		errCh := completeAsync(abe, t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		select {
		case completed := <-turn.callback:
			assert.True(t, completed)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "the result did not reach the workflow actor")
		}
		requireNoAnswer(t, errCh)

		turn.durable.Settle(nil)
		require.NoError(t, requireAnswer(t, errCh))
	})

	t.Run("a failed save answers Aborted", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1")

		errCh := completeAsync(abe, t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		<-turn.callback
		requireNoAnswer(t, errCh)

		turn.durable.Settle(errors.New("state store unavailable"))
		err := requireAnswer(t, errCh)
		require.Error(t, err)
		assert.Equal(t, codes.Aborted, status.Code(err))
		assert.Contains(t, err.Error(), "state store unavailable")
		assert.False(t, abe.durables.isSaved("wf1", "t1"))
	})

	t.Run("a saved token sent again answers success", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1")

		resp := &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"}
		errCh := completeAsync(abe, t.Context(), resp)
		<-turn.callback
		turn.durable.Settle(nil)
		require.NoError(t, requireAnswer(t, errCh))

		require.NoError(t, abe.CompleteWorkflowTask(t.Context(), resp))
	})

	t.Run("a token that was not saved answers NotFound", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()

		err := abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))

		turn := startTurn(t, abe, "wf1")
		errCh := completeAsync(abe, t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		<-turn.callback
		turn.durable.Settle(errors.New("state store unavailable"))
		require.Error(t, requireAnswer(t, errCh))

		err = abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("a result without a token for no pending task answers success", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		require.NoError(t, abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1"}))
	})

	t.Run("a token sent again while its save runs waits for the save", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1")

		resp := &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"}
		first := completeAsync(abe, t.Context(), resp)
		<-turn.callback
		require.Eventually(t, func() bool {
			return abe.durables.consumedWith("wf1", "t1") != nil
		}, 5*time.Second, 10*time.Millisecond)

		second := completeAsync(abe, t.Context(), resp)
		requireNoAnswer(t, first)
		requireNoAnswer(t, second)

		turn.durable.Settle(nil)
		require.NoError(t, requireAnswer(t, first))
		require.NoError(t, requireAnswer(t, second))
	})

	t.Run("a cancelled call stops waiting", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		turn := startTurn(t, abe, "wf1")

		ctx, cancel := context.WithCancel(t.Context())
		errCh := completeAsync(abe, ctx, &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"})
		<-turn.callback
		requireNoAnswer(t, errCh)

		cancel()
		err := requireAnswer(t, errCh)
		assert.Equal(t, codes.Canceled, status.Code(err))
	})

	t.Run("a result no tracked work item takes answers at once", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()

		delivered := make(chan struct{}, 1)
		dereg := abe.OnWorkflowTaskCompletion(&protos.WorkflowRequest{InstanceId: "wf1"}, func(*protos.WorkflowResponse, error) {
			delivered <- struct{}{}
		})
		defer dereg()

		require.NoError(t, abe.CompleteWorkflowTask(t.Context(), &protos.WorkflowResponse{InstanceId: "wf1", CompletionToken: "t1"}))
		<-delivered
	})
}

func TestDurablesForgetsSettledWorkItems(t *testing.T) {
	t.Parallel()

	d := newDurables(clocktesting.NewFakeClock(time.Now()))
	durable := todo.NewDurable()
	d.track("wf1", durable)
	require.Len(t, d.unconsumed("wf1"), 1)

	durable.Settle(nil)
	assert.Empty(t, d.unconsumed("wf1"))
	d.lock.Lock()
	assert.Empty(t, d.pending)
	d.lock.Unlock()
}

func TestDurablesSavedTokenExpiry(t *testing.T) {
	t.Parallel()

	clk := clocktesting.NewFakeClock(time.Now())
	d := newDurables(clk)

	d.markSaved("wf1", "t1")
	assert.True(t, d.isSaved("wf1", "t1"))
	assert.False(t, d.isSaved("wf1", "t2"))

	clk.Step(savedTokenTTL)
	d.markSaved("wf2", "t2")
	assert.True(t, d.isSaved("wf1", "t1"), "a token is kept for at least savedTokenTTL")

	clk.Step(savedTokenTTL)
	d.markSaved("wf3", "t3")
	assert.False(t, d.isSaved("wf1", "t1"))
	assert.True(t, d.isSaved("wf2", "t2"))
}

func TestCancelTaskIgnoresUnknownTask(t *testing.T) {
	t.Parallel()

	abe := newDurableBackend()
	require.NoError(t, abe.CancelWorkflowTask(t.Context(), "wf1"))
	require.NoError(t, abe.CancelActivityTask(t.Context(), "wf1", 1))
}
