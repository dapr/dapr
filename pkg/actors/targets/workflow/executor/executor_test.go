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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/executor/pending"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// newClaimTestExecutor builds an executor without the factory goroutines so
// deactivation requests can be asserted on the buffered channel directly. The
// workflow-shaped actor ID has no sibling rendezvous form, keeping complete()
// off the forwarding path, which needs a live actor router.
func newClaimTestExecutor(t *testing.T) (*executor, *factory) {
	t.Helper()
	f := &factory{
		actorType:    "dapr.internal.default.test.executor",
		deactivateCh: make(chan *executor, 10),
		pending:      pending.New(),
	}
	e, ok := f.GetOrCreate("abc").(*executor)
	require.True(t, ok)
	return e, f
}

func claimReq(taskType string) *internalsv1pb.InternalInvokeRequest {
	return internalsv1pb.NewInternalInvokeRequest(MethodClaim).
		WithActor("dapr.internal.default.test.executor", "abc").
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{MetadataTaskType: {taskType}})
}

func completeReq(taskType string, data []byte) *internalsv1pb.InternalInvokeRequest {
	return internalsv1pb.NewInternalInvokeRequest(MethodComplete).
		WithActor("dapr.internal.default.test.executor", "abc").
		WithData(data).
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{MetadataTaskType: {taskType}})
}

func cancelReq(taskType string) *internalsv1pb.InternalInvokeRequest {
	return internalsv1pb.NewInternalInvokeRequest(MethodCancel).
		WithActor("dapr.internal.default.test.executor", "abc").
		WithContentType(invokev1.ProtobufContentType).
		WithMetadata(map[string][]string{MetadataTaskType: {taskType}})
}

// Test_claimCompleteRace hammers the interleaving where a waiter's whole
// Register+Claim lands between a completer's pending-map miss and its channel
// park: without the executor's rendezvous mutex the claim can observe an
// empty channel, the park lands afterwards with no reader, and the waiter
// hangs. The window is a few instructions wide, so this cannot reliably
// reproduce the unsynchronized bug; the guarantee is the mu critical
// sections in complete/claim. This keeps regression pressure on the
// invariant: every iteration must resolve the waiter via either the claim or
// the pending map, whichever side wins the race.
func Test_claimCompleteRace(t *testing.T) {
	t.Parallel()

	for range 10_000 {
		f := &factory{
			actorType:    "dapr.internal.default.test.executor",
			deactivateCh: make(chan *executor, 10),
			pending:      pending.New(),
		}
		e, ok := f.GetOrCreate("abc").(*executor)
		require.True(t, ok)

		completerDone := make(chan error, 1)
		go func() {
			_, err := e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("payload")))
			completerDone <- err
		}()

		waitCh := make(chan pending.Result, 1)
		deregister := f.pending.RegisterCallback(PendingKey(TaskTypeWorkflow, "abc"), func(r pending.Result) {
			select {
			case waitCh <- r:
			default:
			}
		})

		res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
		require.NoError(t, err)

		switch res.GetStatus().GetCode() {
		case int32(codes.OK):
			assert.Equal(t, []byte("payload"), res.GetMessage().GetData().GetValue())
		case int32(codes.NotFound):
			select {
			case pres := <-waitCh:
				assert.Equal(t, []byte("payload"), pres.Data)
			case <-time.After(time.Second * 5):
				t.Fatal("completion neither claimed nor delivered to the pending map waiter")
			}
		default:
			t.Fatalf("unexpected claim status %d", res.GetStatus().GetCode())
		}

		deregister()
		require.NoError(t, <-completerDone)
	}
}

func Test_claim(t *testing.T) {
	t.Parallel()

	t.Run("not found when nothing parked, actor deactivates", func(t *testing.T) {
		t.Parallel()
		e, f := newClaimTestExecutor(t)

		res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.NotFound), res.GetStatus().GetCode())
		assert.Len(t, f.deactivateCh, 1)
	})

	t.Run("returns completion parked before registration", func(t *testing.T) {
		t.Parallel()
		e, f := newClaimTestExecutor(t)

		_, err := e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("payload")))
		require.NoError(t, err)

		res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.OK), res.GetStatus().GetCode())
		assert.Equal(t, []byte("payload"), res.GetMessage().GetData().GetValue())
		assert.Equal(t, TaskTypeWorkflow, res.GetHeaders()[MetadataTaskType].GetValues()[0])
		assert.Len(t, f.deactivateCh, 1)

		res, err = e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.NotFound), res.GetStatus().GetCode())
	})

	t.Run("aborted when cancellation parked before registration", func(t *testing.T) {
		t.Parallel()
		e, f := newClaimTestExecutor(t)

		_, err := e.InvokeMethod(t.Context(), cancelReq(TaskTypeActivity))
		require.NoError(t, err)

		res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeActivity))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.Aborted), res.GetStatus().GetCode())
		assert.Len(t, f.deactivateCh, 1)
	})

	t.Run("parked completion takes precedence over cancellation", func(t *testing.T) {
		t.Parallel()
		e, _ := newClaimTestExecutor(t)

		_, err := e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("payload")))
		require.NoError(t, err)
		_, err = e.InvokeMethod(t.Context(), cancelReq(TaskTypeWorkflow))
		require.NoError(t, err)

		res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.OK), res.GetStatus().GetCode())
		assert.Equal(t, []byte("payload"), res.GetMessage().GetData().GetValue())
	})

	t.Run("other task type's completion is put back, not claimed", func(t *testing.T) {
		t.Parallel()
		e, f := newClaimTestExecutor(t)

		_, err := e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("payload")))
		require.NoError(t, err)

		res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeActivity))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.NotFound), res.GetStatus().GetCode())
		assert.Empty(t, f.deactivateCh, "put-back must keep the actor alive for its own waiter")

		res, err = e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.OK), res.GetStatus().GetCode())
		assert.Equal(t, []byte("payload"), res.GetMessage().GetData().GetValue())
	})

	t.Run("displaced completion is claimed before the channel", func(t *testing.T) {
		t.Parallel()
		e, _ := newClaimTestExecutor(t)

		_, err := e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("first")))
		require.NoError(t, err)

		// The wrong-type claim moves "first" to the displaced slot, freeing
		// the channel for "second".
		res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeActivity))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.NotFound), res.GetStatus().GetCode())

		_, err = e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("second")))
		require.NoError(t, err)

		for _, want := range [][]byte{[]byte("first"), []byte("second")} {
			res, err = e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
			require.NoError(t, err)
			assert.Equal(t, int32(codes.OK), res.GetStatus().GetCode())
			assert.Equal(t, want, res.GetMessage().GetData().GetValue())
		}
	})

	t.Run("no completion is lost to a concurrent blocked completer", func(t *testing.T) {
		t.Parallel()
		e, f := newClaimTestExecutor(t)

		// Fill the channel slot with a workflow-type completion, then block an
		// activity-type completer on the full channel: the moment a claim
		// drains the slot this sender refills it, which is exactly the window
		// where a channel put-back would drop the drained payload. The two
		// payloads are of different task types, so neither may ever be lost.
		_, err := e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("wf")))
		require.NoError(t, err)
		blockedDone := make(chan error, 1)
		go func() {
			_, berr := e.InvokeMethod(t.Context(), completeReq(TaskTypeActivity, []byte("act")))
			blockedDone <- berr
		}()

		// The activity claim displaces "wf"; depending on whether the blocked
		// sender has refilled the slot yet it returns "act" or not-found.
		res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeActivity))
		require.NoError(t, err)
		gotAct := res.GetStatus().GetCode() == int32(codes.OK)
		if gotAct {
			assert.Equal(t, []byte("act"), res.GetMessage().GetData().GetValue())
		}
		assert.Empty(t, f.deactivateCh, "a displaced completion must keep the actor alive")

		res, err = e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.OK), res.GetStatus().GetCode())
		assert.Equal(t, []byte("wf"), res.GetMessage().GetData().GetValue())

		if !gotAct {
			assert.EventuallyWithT(t, func(col *assert.CollectT) {
				cres, cerr := e.InvokeMethod(t.Context(), claimReq(TaskTypeActivity))
				if !assert.NoError(col, cerr) {
					return
				}
				if assert.Equal(col, int32(codes.OK), cres.GetStatus().GetCode()) {
					assert.Equal(col, []byte("act"), cres.GetMessage().GetData().GetValue())
				}
			}, time.Second*5, time.Millisecond)
		}
		require.NoError(t, <-blockedDone)
	})

	t.Run("delivers to registered waiter instead of parking", func(t *testing.T) {
		t.Parallel()
		e, f := newClaimTestExecutor(t)

		waitCh := make(chan pending.Result, 1)
		deregister := f.pending.RegisterCallback(PendingKey(TaskTypeWorkflow, "abc"), func(r pending.Result) {
			select {
			case waitCh <- r:
			default:
			}
		})
		defer deregister()

		_, err := e.InvokeMethod(t.Context(), completeReq(TaskTypeWorkflow, []byte("payload")))
		require.NoError(t, err)

		select {
		case res := <-waitCh:
			assert.Equal(t, []byte("payload"), res.Data)
		default:
			t.Fatal("completion was not delivered to the registered waiter")
		}

		// A successful delivery also parks a copy for stale watch streams;
		// a subsequent claim drains that copy (duplicates are discarded by
		// the workflow-side dedup guards).
		res, err := e.InvokeMethod(t.Context(), claimReq(TaskTypeWorkflow))
		require.NoError(t, err)
		assert.Equal(t, int32(codes.OK), res.GetStatus().GetCode())
		assert.Equal(t, []byte("payload"), res.GetMessage().GetData().GetValue())
	})
}
