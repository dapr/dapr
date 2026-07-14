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

package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/payloadstore"
	payloadstorefake "github.com/dapr/durabletask-go/backend/payloadstore/fake"
)

// resultEvent returns a TaskCompleted history event carrying result as its
// user payload. The fixed timestamp keeps marshaled bytes comparable
// across identically built events.
func resultEvent(id int32, result string) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.New(time.Date(2026, 7, 8, 10, 0, int(id), 0, time.UTC)),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: id,
				Result:          wrapperspb.String(result),
			},
		},
	}
}

func eventPayloadValue(t *testing.T, e *backend.HistoryEvent) string {
	t.Helper()
	p := payloadstore.Payload(e)
	require.NotNil(t, p)
	return p.GetValue()
}

// requireOffloaded asserts the event's payload was replaced by a
// reference resolving to original through store.
func requireOffloaded(t *testing.T, e *backend.HistoryEvent, store payloadstore.Store, original string) {
	t.Helper()

	v := eventPayloadValue(t, e)
	require.True(t, payloadstore.IsReference(v), "payload was not replaced with a reference")

	ref, err := payloadstore.DecodeReference(v)
	require.NoError(t, err)
	assert.Equal(t, uint64(len(original)), ref.Size)

	data, err := store.Get(t.Context(), "wf1", ref)
	require.NoError(t, err)
	assert.Equal(t, original, string(data))
}

func TestOffloadNewPayloads_ThresholdBoundary(t *testing.T) {
	t.Parallel()

	const threshold = 10
	below := strings.Repeat("b", threshold-1)
	at := strings.Repeat("a", threshold)
	above := strings.Repeat("A", threshold+1)

	s := NewState(testOpts())
	s.AddToHistory(resultEvent(0, below))
	s.AddToHistory(resultEvent(1, at))
	s.AddToHistory(resultEvent(2, above))

	store := payloadstorefake.New().WithThreshold(threshold)
	require.NoError(t, s.OffloadNewPayloads(t.Context(), store, "wf1"))

	assert.Equal(t, below, eventPayloadValue(t, s.History[0]), "below-threshold payload must stay inline")
	requireOffloaded(t, s.History[1], store, at)
	requireOffloaded(t, s.History[2], store, above)
}

func TestOffloadNewPayloads_CoversInboxAndHistory(t *testing.T) {
	t.Parallel()

	const payload = "0123456789abcdef"

	s := NewState(testOpts())
	s.AddToInbox(&backend.HistoryEvent{
		EventId: -1,
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "wf",
				Input: wrapperspb.String(payload),
			},
		},
	})
	s.AddToHistory(resultEvent(0, payload))

	store := payloadstorefake.New().WithThreshold(8)
	require.NoError(t, s.OffloadNewPayloads(t.Context(), store, "wf1"))

	requireOffloaded(t, s.Inbox[0], store, payload)
	requireOffloaded(t, s.History[0], store, payload)
}

func TestOffloadNewPayloads_TouchesOnlyNewEvents(t *testing.T) {
	t.Parallel()

	const oldPayload = "old event payload"
	const newPayload = "new event payload"

	s := NewState(testOpts())
	s.AddToHistory(resultEvent(0, oldPayload))
	// Simulate a state that was persisted and reloaded: the existing
	// history event is no longer "new". Persisted events are immutable
	// (and, with signing, covered by signatures), so offload must not
	// touch them even when their payload exceeds the threshold.
	s.ResetChangeTracking()
	s.AddToHistory(resultEvent(1, newPayload))

	store := payloadstorefake.New().WithThreshold(1)
	require.NoError(t, s.OffloadNewPayloads(t.Context(), store, "wf1"))

	assert.Equal(t, oldPayload, eventPayloadValue(t, s.History[0]), "persisted event must not be touched")
	requireOffloaded(t, s.History[1], store, newPayload)
}

func TestOffloadNewPayloads_Idempotent(t *testing.T) {
	t.Parallel()

	const payload = "a payload well above the threshold"

	s := NewState(testOpts())
	s.AddToHistory(resultEvent(0, payload))

	store := payloadstorefake.New().WithThreshold(4)
	require.NoError(t, s.OffloadNewPayloads(t.Context(), store, "wf1"))

	encodedOnce := eventPayloadValue(t, s.History[0])
	putsAfterFirst := store.PutCalls()

	// A retried turn re-runs offload over the same events. Values already
	// encoding a reference must be skipped without another Put.
	require.NoError(t, s.OffloadNewPayloads(t.Context(), store, "wf1"))

	assert.Equal(t, encodedOnce, eventPayloadValue(t, s.History[0]), "re-running offload must not double-encode")
	assert.Equal(t, putsAfterFirst, store.PutCalls(), "re-running offload must not Put again")
}

// TestOffloadNewPayloads_ManyEvents drives the concurrent Put fan-out
// (run with -race to catch unsynchronized event mutation).
func TestOffloadNewPayloads_ManyEvents(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())
	payloads := make([]string, 24)
	for i := range payloads {
		payloads[i] = fmt.Sprintf("payload-%02d-", i) + strings.Repeat("p", 64)
		s.AddToHistory(resultEvent(int32(i), payloads[i]))
	}

	store := payloadstorefake.New().WithThreshold(8)
	require.NoError(t, s.OffloadNewPayloads(t.Context(), store, "wf1"))

	for i := range payloads {
		requireOffloaded(t, s.History[i], store, payloads[i])
	}
	assert.Equal(t, len(payloads), store.PutCalls())
}

func TestOffloadNewPayloads_PutFailureFailsWholePass(t *testing.T) {
	t.Parallel()

	errPut := errors.New("store unavailable")

	s := NewState(testOpts())
	s.AddToHistory(resultEvent(0, "payload big enough to offload"))

	store := payloadstorefake.New().
		WithThreshold(4).
		WithPutFn(func(context.Context, string, []byte) (payloadstore.Reference, error) {
			return payloadstore.Reference{}, errPut
		})

	err := s.OffloadNewPayloads(t.Context(), store, "wf1")
	require.ErrorIs(t, err, errPut)
}

func TestOffloadNewPayloads_SkipsEventsWithoutPayload(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())
	// No payload field at all.
	s.AddToHistory(testEvent(0))
	// Payload field present but unset.
	s.AddToHistory(&backend.HistoryEvent{
		EventId: 1,
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act"},
		},
	})

	store := payloadstorefake.New().WithThreshold(1)
	require.NoError(t, s.OffloadNewPayloads(t.Context(), store, "wf1"))
	assert.Zero(t, store.PutCalls())
}

func TestOffloadNewPayloads_UserDataCraftedAsReference(t *testing.T) {
	t.Parallel()

	// A payload that fully decodes as a reference is indistinguishable
	// from an already-offloaded value and must be skipped verbatim.
	crafted := payloadstore.EncodeReference(payloadstore.Reference{Key: "not-in-store", Size: 999})
	// A payload that only carries the magic prefix but fails a strict
	// decode is user data and must be offloaded like any other payload,
	// preserving the original bytes through the store.
	corrupt := crafted[:len(crafted)-3] + "garbage-tail-to-break-the-json-body"

	s := NewState(testOpts())
	s.AddToHistory(resultEvent(0, crafted))
	s.AddToHistory(resultEvent(1, corrupt))

	store := payloadstorefake.New().WithThreshold(4)
	require.NoError(t, s.OffloadNewPayloads(t.Context(), store, "wf1"))

	assert.Equal(t, crafted, eventPayloadValue(t, s.History[0]), "well-formed crafted reference must be skipped verbatim")
	requireOffloaded(t, s.History[1], store, corrupt)
	assert.Equal(t, 1, store.PutCalls())
}

// TestOffloadNewPayloads_NoOpBelowThresholdIsByteIdentical asserts the
// no-offload path changes nothing about what gets persisted: a state that
// ran the offload pass but had nothing to offload produces a save request
// byte-identical to one that never ran it.
func TestOffloadNewPayloads_NoOpBelowThresholdIsByteIdentical(t *testing.T) {
	t.Parallel()

	build := func() *State {
		s := NewState(testOpts())
		s.AddToInbox(resultEvent(0, "small"))
		s.AddToHistory(resultEvent(1, "also small"))
		return s
	}

	offloaded := build()
	require.NoError(t, offloaded.OffloadNewPayloads(t.Context(), payloadstorefake.New(), "wf1"))
	baseline := build()

	reqOffloaded, err := offloaded.GetSaveRequest("actor1")
	require.NoError(t, err)
	reqBaseline, err := baseline.GetSaveRequest("actor1")
	require.NoError(t, err)

	require.Len(t, reqOffloaded.Operations, len(reqBaseline.Operations))
	for i := range reqBaseline.Operations {
		assert.Equal(t, reqBaseline.Operations[i], reqOffloaded.Operations[i])
	}
}

// TestOffloadNewPayloads_PersistLoadRoundTrip drives the real save path:
// offload, build the save request, apply it to a map-backed store, load
// the state back, and resolve the persisted reference to the original
// payload.
func TestOffloadNewPayloads_PersistLoadRoundTrip(t *testing.T) {
	t.Parallel()

	const actorID = "wf-offload-roundtrip"
	payload := strings.Repeat("x", 64)

	s := NewState(testOpts())
	s.AddToInbox(&backend.HistoryEvent{
		EventId: -1,
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "wf",
				Input: wrapperspb.String(payload),
			},
		},
	})
	s.AddToHistory(resultEvent(0, payload))

	store := payloadstorefake.New().WithThreshold(16)
	require.NoError(t, s.OffloadNewPayloads(t.Context(), store, actorID))

	req, err := s.GetSaveRequest(actorID)
	require.NoError(t, err)

	// Apply the transactional request to a plain key-value map, the way a
	// state store would.
	kv := make(map[string][]byte)
	for _, op := range req.Operations {
		switch typed := op.Request.(type) {
		case api.TransactionalUpsert:
			data, ok := typed.Value.([]byte)
			require.True(t, ok)
			kv[typed.Key] = data
		case api.TransactionalDelete:
			delete(kv, typed.Key)
		default:
			t.Fatalf("unexpected operation type %T", op.Request)
		}
	}

	// The persisted bytes must already carry references.
	var persisted backend.HistoryEvent
	require.NoError(t, proto.Unmarshal(kv["history-000000"], &persisted))
	require.True(t, payloadstore.IsReference(persisted.GetTaskCompleted().GetResult().GetValue()),
		"persisted history bytes must carry the reference, not the payload")

	actorState := statefake.New().
		WithGetFn(func(_ context.Context, req *api.GetStateRequest, _ bool) (*api.StateResponse, error) {
			return &api.StateResponse{Data: kv[req.Key]}, nil
		}).
		WithGetBulkFn(func(_ context.Context, req *api.GetBulkStateRequest, _ bool) (api.BulkStateResponse, error) {
			out := api.BulkStateResponse{}
			for _, k := range req.Keys {
				out[k] = api.BulkStateEntry{Data: kv[k]}
			}
			return out, nil
		})

	loaded, err := LoadWorkflowState(t.Context(), actorState, actorID, testOpts())
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, loaded.Inbox, 1)
	require.Len(t, loaded.History, 1)

	requireOffloaded(t, loaded.Inbox[0], store, payload)
	requireOffloaded(t, loaded.History[0], store, payload)
}
