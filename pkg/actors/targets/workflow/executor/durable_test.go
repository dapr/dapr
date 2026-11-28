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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor/pending"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

// A forwarded workflow completion is answered once its result is saved. An
// activity completion is answered at once, even when a workflow's Durable is
// handed over under the same key.
func TestCompleteWaitsForWorkflowSave(t *testing.T) {
	t.Parallel()

	complete := func(t *testing.T, taskType string, turn *todo.Durable) <-chan *internalsv1pb.InternalInvokeResponse {
		t.Helper()
		durables := todo.NewDurables()
		f := &factory{
			actorType:    "dapr.internal.default.test.executor",
			deactivateCh: make(chan *executor, 10),
			pending:      pending.New(),
			durables:     durables,
		}
		dereg := f.pending.RegisterCallback(PendingKey(taskType, "abc"), func(pending.Result) {
			durables.Fulfil("abc", "", turn)
		})
		t.Cleanup(dereg)

		req := internalsv1pb.NewInternalInvokeRequest(MethodComplete).
			WithActor(f.actorType, "abc").
			WithData([]byte("result")).
			WithContentType(invokev1.ProtobufContentType).
			WithMetadata(map[string][]string{MetadataTaskType: {taskType}})

		resCh := make(chan *internalsv1pb.InternalInvokeResponse, 1)
		go func() {
			res, err := f.GetOrCreate("abc").InvokeMethod(context.Background(), req)
			assert.NoError(t, err)
			resCh <- res
		}()
		return resCh
	}

	t.Run("a workflow completion waits for the save", func(t *testing.T) {
		t.Parallel()
		turn := todo.NewDurable()
		resCh := complete(t, TaskTypeWorkflow, turn)
		select {
		case <-resCh:
			require.FailNow(t, "answered before the save")
		case <-time.After(200 * time.Millisecond):
		}
		turn.Settle(nil)
		select {
		case res := <-resCh:
			assert.Equal(t, []string{"true"}, res.GetHeaders()[MetadataSaved].GetValues())
		case <-time.After(5 * time.Second):
			require.FailNow(t, "not answered after the save")
		}
	})

	t.Run("an activity completion is answered at once", func(t *testing.T) {
		t.Parallel()
		resCh := complete(t, TaskTypeActivity, todo.NewDurable())
		select {
		case res := <-resCh:
			assert.Nil(t, res.GetHeaders()[MetadataSaved])
		case <-time.After(5 * time.Second):
			require.FailNow(t, "the activity completion waited for a workflow save")
		}
	})
}
