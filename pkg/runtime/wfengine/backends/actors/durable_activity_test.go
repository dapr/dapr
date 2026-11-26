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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// startActivity hands an activity work item, dispatched with completion
// token t1, to a stand-in for the engine, as startTurn does for a workflow
// turn.
func startActivity(t *testing.T, abe *Actors, instanceID string, taskID int32) *testTurn {
	t.Helper()

	execution := &testTurn{durable: todo.NewDurable(), callback: make(chan bool, 1)}
	wi := &backend.ActivityWorkItem{
		InstanceID: api.InstanceID(instanceID),
		NewEvent:   &protos.HistoryEvent{EventId: taskID},
		Properties: map[string]any{
			todo.CallbackChannelProperty: execution.callback,
			todo.DurableProperty:         execution.durable,
		},
	}

	req := &protos.ActivityRequest{
		WorkflowInstance: &protos.WorkflowInstance{InstanceId: instanceID},
		TaskId:           taskID,
	}
	var (
		once  sync.Once
		lock  sync.Mutex
		dereg func()
	)
	lock.Lock()
	dereg = abe.OnActivityCompletion(req, func(*protos.ActivityResponse, error) {
		once.Do(func() {
			lock.Lock()
			dereg()
			lock.Unlock()
			wi.CompletionToken = "t1"
			assert.NoError(t, abe.CompleteActivityWorkItem(t.Context(), wi))
		})
	})
	lock.Unlock()
	return execution
}

func durableContext(ctx context.Context) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs(todo.DurableHeader, "true"))
}

func completeActivityAsync(abe *Actors, ctx context.Context, resp *protos.ActivityResponse) <-chan error {
	errCh := make(chan error, 1)
	go func() { errCh <- abe.CompleteActivityTask(ctx, resp) }()
	return errCh
}

func TestCompleteActivityTaskWaitsForPublish(t *testing.T) {
	t.Parallel()

	resp := &protos.ActivityResponse{InstanceId: "wf1", TaskId: 3, CompletionToken: "t1"}

	t.Run("without the header the answer does not wait", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		execution := startActivity(t, abe, "wf1", 3)

		require.NoError(t, abe.CompleteActivityTask(t.Context(), resp))
		assert.True(t, <-execution.callback)

		// Without the header an unknown task is still ignored.
		require.NoError(t, abe.CompleteActivityTask(t.Context(), &protos.ActivityResponse{InstanceId: "wf1", TaskId: 4, CompletionToken: "t2"}))
	})

	t.Run("with the header the answer waits for the publish", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		execution := startActivity(t, abe, "wf1", 3)

		errCh := completeActivityAsync(abe, durableContext(t.Context()), resp)
		assert.True(t, <-execution.callback)
		requireNoAnswer(t, errCh)

		execution.durable.Settle(nil)
		require.NoError(t, requireAnswer(t, errCh))
	})

	t.Run("a result without a completion token waits for the publish", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		execution := startActivity(t, abe, "wf1", 3)

		errCh := completeActivityAsync(abe, durableContext(t.Context()), &protos.ActivityResponse{InstanceId: "wf1", TaskId: 3})
		assert.True(t, <-execution.callback)
		requireNoAnswer(t, errCh)

		execution.durable.Settle(nil)
		require.NoError(t, requireAnswer(t, errCh))
	})

	t.Run("a failed publish answers Aborted", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		execution := startActivity(t, abe, "wf1", 3)

		errCh := completeActivityAsync(abe, durableContext(t.Context()), resp)
		<-execution.callback
		requireNoAnswer(t, errCh)

		execution.durable.Settle(errors.New("parent workflow unreachable"))
		assert.Equal(t, codes.Aborted, status.Code(requireAnswer(t, errCh)))
	})

	t.Run("a different task of the same instance is not waited for", func(t *testing.T) {
		t.Parallel()
		abe := newDurableBackend()
		other := startActivity(t, abe, "wf1", 4)
		execution := startActivity(t, abe, "wf1", 3)

		errCh := completeActivityAsync(abe, durableContext(t.Context()), &protos.ActivityResponse{InstanceId: "wf1", TaskId: 3})
		<-execution.callback
		execution.durable.Settle(nil)
		require.NoError(t, requireAnswer(t, errCh))
		select {
		case <-other.callback:
			require.FailNow(t, "the other task must not take the result")
		default:
		}
	})
}
