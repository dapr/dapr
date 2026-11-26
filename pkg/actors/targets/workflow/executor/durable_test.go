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

package executor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor/pending"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

// A completion forwarded to the daprd that owns its task is answered once
// the result is saved: always for a workflow, and for an activity only when
// the worker asked to wait.
func TestCompleteWaitsForSave(t *testing.T) {
	t.Parallel()

	// complete delivers a forwarded completion of taskType with token t1 to
	// an executor whose waiter hands over turn, and returns its answer.
	complete := func(t *testing.T, taskType string, await bool, turn *todo.Durable) <-chan *internalsv1pb.InternalInvokeResponse {
		t.Helper()
		durables := todo.NewDurables()
		f := &factory{
			actorType:    "dapr.internal.default.test.executor",
			deactivateCh: make(chan *executor, 10),
			pending:      pending.New(),
			durables:     durables,
		}
		dereg := f.pending.RegisterCallback(PendingKey(taskType, "abc"), func(pending.Result) {
			durables.Fulfil("abc", "t1", turn)
		})
		t.Cleanup(dereg)

		md := map[string][]string{MetadataTaskType: {taskType}, MetadataCompletionToken: {"t1"}}
		if await {
			md[MetadataAwaitSaved] = []string{"true"}
		}
		req := internalsv1pb.NewInternalInvokeRequest(MethodComplete).
			WithActor(f.actorType, "abc").
			WithData([]byte("result")).
			WithContentType(invokev1.ProtobufContentType).
			WithMetadata(md)

		resCh := make(chan *internalsv1pb.InternalInvokeResponse, 1)
		go func() {
			res, err := f.GetOrCreate("abc").InvokeMethod(context.Background(), req)
			assert.NoError(t, err)
			resCh <- res
		}()
		return resCh
	}

	saved := func(res *internalsv1pb.InternalInvokeResponse) []string {
		return res.GetHeaders()[MetadataSaved].GetValues()
	}

	requireWaiting := func(t *testing.T, resCh <-chan *internalsv1pb.InternalInvokeResponse) {
		t.Helper()
		select {
		case <-resCh:
			require.FailNow(t, "answered before the result was saved")
		case <-time.After(200 * time.Millisecond):
		}
	}

	requireAnswer := func(t *testing.T, resCh <-chan *internalsv1pb.InternalInvokeResponse) *internalsv1pb.InternalInvokeResponse {
		t.Helper()
		select {
		case res := <-resCh:
			return res
		case <-time.After(5 * time.Second):
			require.FailNow(t, "not answered")
			return nil
		}
	}

	t.Run("a workflow result waits for the save", func(t *testing.T) {
		t.Parallel()
		turn := todo.NewDurable()
		resCh := complete(t, TaskTypeWorkflow, false, turn)
		requireWaiting(t, resCh)

		turn.Settle(nil)
		assert.Equal(t, []string{"true"}, saved(requireAnswer(t, resCh)))
	})

	t.Run("an activity result does not wait unless asked to", func(t *testing.T) {
		t.Parallel()
		resCh := complete(t, TaskTypeActivity, false, todo.NewDurable())
		assert.Empty(t, saved(requireAnswer(t, resCh)))
	})

	t.Run("an activity result asked to wait reports an unsaved result", func(t *testing.T) {
		t.Parallel()
		turn := todo.NewDurable()
		resCh := complete(t, TaskTypeActivity, true, turn)
		requireWaiting(t, resCh)

		turn.Settle(errors.New("parent workflow unreachable"))
		assert.Equal(t, []string{"false"}, saved(requireAnswer(t, resCh)))
	})
}
