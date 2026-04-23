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
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func addSig(t *testing.T, s *State, sig *backend.HistorySignature) {
	t.Helper()
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(sig)
	require.NoError(t, err)
	s.AddSignature(sig, raw)
}

func testOpts() Options {
	return Options{
		AppID:             "test-app",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	}
}

func testEvent(id int32) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_WorkflowStarted{
			WorkflowStarted: &protos.WorkflowStartedEvent{},
		},
	}
}

func TestGetMultiEntryKeyName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prefix string
		index  uint64
		want   string
	}{
		{"history", 0, "history-000000"},
		{"history", 1, "history-000001"},
		{"history", 42, "history-000042"},
		{"history", 999999, "history-999999"},
		{"history", 1000000, "history-1000000"},
		{"inbox", 5, "inbox-000005"},
		{"sigcert", 0, "sigcert-000000"},
		{"signature", 123, "signature-000123"},
	}

	for _, tc := range tests {
		got := getMultiEntryKeyName(tc.prefix, tc.index)
		assert.Equal(t, tc.want, got, "prefix=%s index=%d", tc.prefix, tc.index)
	}
}

func TestNewState(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	assert.Equal(t, uint64(1), s.Generation)
	assert.Empty(t, s.Inbox)
	assert.Empty(t, s.History)
	assert.Empty(t, s.SigningCertificates)
	assert.Empty(t, s.Signatures)
}

func TestAddToHistory(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	e1 := testEvent(0)
	e2 := testEvent(1)
	s.AddToHistory(e1)
	s.AddToHistory(e2)

	assert.Len(t, s.History, 2)
	assert.Equal(t, 2, s.historyAddedCount)
}

func TestAddSigningCertificateAndSignature(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	cert := &backend.SigningCertificate{Certificate: []byte("cert-data")}
	sig := &backend.HistorySignature{
		StartEventIndex: 0,
		EventCount:      1,
		Signature:       []byte("sig-data"),
	}

	s.AddSigningCertificate(cert)
	addSig(t, s, sig)

	assert.Len(t, s.SigningCertificates, 1)
	assert.Equal(t, 1, s.signingCertificatesAddedCount)
	assert.Len(t, s.Signatures, 1)
	assert.Equal(t, 1, s.signaturesAddedCount)
}

func TestReset(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	s.AddToInbox(testEvent(0))
	s.AddToHistory(testEvent(0))
	s.AddToHistory(testEvent(1))
	s.AddSigningCertificate(&backend.SigningCertificate{Certificate: []byte("cert")})
	addSig(t, s, &backend.HistorySignature{Signature: []byte("sig")})
	s.CustomStatus = wrapperspb.String("running")

	s.Reset()

	assert.Nil(t, s.Inbox)
	assert.Nil(t, s.History)
	assert.Nil(t, s.SigningCertificates)
	assert.Nil(t, s.Signatures)
	assert.Nil(t, s.CustomStatus)
	assert.Equal(t, uint64(2), s.Generation)

	// Removed counts should reflect the items that were cleared.
	assert.Equal(t, 1, s.inboxRemovedCount)
	assert.Equal(t, 2, s.historyRemovedCount)
	assert.Equal(t, 1, s.signingCertificatesRemovedCount)
	assert.Equal(t, 1, s.signaturesRemovedCount)

	// Added counts should be zeroed.
	assert.Equal(t, 0, s.inboxAddedCount)
	assert.Equal(t, 0, s.historyAddedCount)
	assert.Equal(t, 0, s.signingCertificatesAddedCount)
	assert.Equal(t, 0, s.signaturesAddedCount)
}

func TestResetChangeTracking(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	s.AddToHistory(testEvent(0))
	s.AddSigningCertificate(&backend.SigningCertificate{Certificate: []byte("cert")})
	addSig(t, s, &backend.HistorySignature{Signature: []byte("sig")})
	s.SetMarshaledNewHistory([][]byte{{1, 2, 3}})

	s.ResetChangeTracking()

	assert.Equal(t, 0, s.historyAddedCount)
	assert.Equal(t, 0, s.historyRemovedCount)
	assert.Equal(t, 0, s.signingCertificatesAddedCount)
	assert.Equal(t, 0, s.signingCertificatesRemovedCount)
	assert.Equal(t, 0, s.signaturesAddedCount)
	assert.Equal(t, 0, s.signaturesRemovedCount)
	assert.Nil(t, s.marshaledNewHistory)
}

func TestApplyRuntimeStateChanges(t *testing.T) {
	t.Parallel()

	t.Run("without continue-as-new", func(t *testing.T) {
		t.Parallel()

		s := NewState(testOpts())
		s.AddToHistory(testEvent(0))
		s.AddSigningCertificate(&backend.SigningCertificate{Certificate: []byte("cert")})
		addSig(t, s, &backend.HistorySignature{Signature: []byte("sig")})

		// Reset tracking so we can see what ApplyRuntimeStateChanges does.
		s.ResetChangeTracking()

		rs := &backend.WorkflowRuntimeState{
			NewEvents: []*backend.HistoryEvent{testEvent(1), testEvent(2)},
		}

		s.ApplyRuntimeStateChanges(rs)

		// History should grow, signing data stays.
		assert.Len(t, s.History, 3)
		assert.Equal(t, 2, s.historyAddedCount)
		assert.Len(t, s.SigningCertificates, 1)
		assert.Len(t, s.Signatures, 1)
	})

	t.Run("with continue-as-new", func(t *testing.T) {
		t.Parallel()

		s := NewState(testOpts())
		s.AddToHistory(testEvent(0))
		s.AddToHistory(testEvent(1))
		s.AddSigningCertificate(&backend.SigningCertificate{Certificate: []byte("cert")})
		addSig(t, s, &backend.HistorySignature{Signature: []byte("sig")})

		// Reset tracking so we can see what ApplyRuntimeStateChanges does.
		s.ResetChangeTracking()

		rs := &backend.WorkflowRuntimeState{
			ContinuedAsNew: true,
			NewEvents:      []*backend.HistoryEvent{testEvent(0)},
		}

		s.ApplyRuntimeStateChanges(rs)

		// Old history and signing data should be cleared.
		assert.Len(t, s.History, 1)
		assert.Equal(t, 1, s.historyAddedCount)
		assert.Equal(t, 2, s.historyRemovedCount)
		assert.Empty(t, s.SigningCertificates)
		assert.Equal(t, 1, s.signingCertificatesRemovedCount)
		assert.Empty(t, s.Signatures)
		assert.Equal(t, 1, s.signaturesRemovedCount)
	})
}

func TestSetMarshaledNewHistory(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())
	raw := [][]byte{{1, 2, 3}, {4, 5, 6}}

	s.SetMarshaledNewHistory(raw)
	assert.Equal(t, raw, s.marshaledNewHistory)
}

func TestGetSaveRequest_HistoryWithMarshaledBytes(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	e := testEvent(0)
	s.AddToHistory(e)

	// Pre-marshal with deterministic marshaling.
	marshaledBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(e)
	require.NoError(t, err)
	s.SetMarshaledNewHistory([][]byte{marshaledBytes})

	req, err := s.GetSaveRequest("actor1")
	require.NoError(t, err)

	// Find the history upsert operation.
	var historyOp *api.TransactionalUpsert
	for _, op := range req.Operations {
		if op.Operation == api.Upsert {
			if u, ok := op.Request.(api.TransactionalUpsert); ok {
				if u.Key == "history-000000" {
					historyOp = &u
					break
				}
			}
		}
	}

	require.NotNil(t, historyOp, "expected history-000000 upsert")
	assert.Equal(t, marshaledBytes, historyOp.Value, "persisted bytes should match pre-marshaled bytes")
}

func TestGetSaveRequest_HistoryWithoutMarshaledBytes(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	e := testEvent(0)
	s.AddToHistory(e)

	// No SetMarshaledNewHistory call — should use proto.Marshal fallback.
	req, err := s.GetSaveRequest("actor1")
	require.NoError(t, err)

	expected, err := proto.Marshal(e)
	require.NoError(t, err)

	var historyOp *api.TransactionalUpsert
	for _, op := range req.Operations {
		if op.Operation == api.Upsert {
			if u, ok := op.Request.(api.TransactionalUpsert); ok {
				if u.Key == "history-000000" {
					historyOp = &u
					break
				}
			}
		}
	}

	require.NotNil(t, historyOp, "expected history-000000 upsert")
	assert.Equal(t, expected, historyOp.Value)
}

func TestGetSaveRequest_SigningDataOperations(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	s.AddToHistory(testEvent(0))
	s.AddSigningCertificate(&backend.SigningCertificate{Certificate: []byte("cert-data")})
	addSig(t, s, &backend.HistorySignature{
		StartEventIndex: 0,
		EventCount:      1,
		Signature:       []byte("sig-data"),
	})

	req, err := s.GetSaveRequest("actor1")
	require.NoError(t, err)

	// Collect operation keys by type.
	upsertKeys := make(map[string]bool)
	for _, op := range req.Operations {
		if op.Operation == api.Upsert {
			if u, ok := op.Request.(api.TransactionalUpsert); ok {
				upsertKeys[u.Key] = true
			}
		}
	}

	assert.True(t, upsertKeys["sigcert-000000"], "expected sigcert upsert")
	assert.True(t, upsertKeys["signature-000000"], "expected signature upsert")
	assert.True(t, upsertKeys["metadata"], "expected metadata upsert")
}

func TestGetSaveRequest_DeletesOnReset(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	// Add history and signing data, then reset to simulate continue-as-new.
	s.AddToHistory(testEvent(0))
	s.AddToHistory(testEvent(1))
	s.AddSigningCertificate(&backend.SigningCertificate{Certificate: []byte("cert")})
	addSig(t, s, &backend.HistorySignature{Signature: []byte("sig1")})
	addSig(t, s, &backend.HistorySignature{Signature: []byte("sig2")})

	s.Reset()

	// Add one new history event after reset.
	s.AddToHistory(testEvent(0))

	req, err := s.GetSaveRequest("actor1")
	require.NoError(t, err)

	deleteKeys := make(map[string]bool)
	upsertKeys := make(map[string]bool)
	for _, op := range req.Operations {
		switch op.Operation {
		case api.Delete:
			if d, ok := op.Request.(api.TransactionalDelete); ok {
				deleteKeys[d.Key] = true
			}
		case api.Upsert:
			if u, ok := op.Request.(api.TransactionalUpsert); ok {
				upsertKeys[u.Key] = true
			}
		}
	}

	// New event at index 0 should be upserted.
	assert.True(t, upsertKeys["history-000000"], "expected history-000000 upsert")

	// Old event at index 1 should be deleted.
	assert.True(t, deleteKeys["history-000001"], "expected history-000001 delete")

	// Old signing data should be deleted.
	assert.True(t, deleteKeys["sigcert-000000"], "expected sigcert-000000 delete")
	assert.True(t, deleteKeys["signature-000000"], "expected signature-000000 delete")
	assert.True(t, deleteKeys["signature-000001"], "expected signature-000001 delete")
}

func TestGetSaveRequest_PreAllocatesOperations(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	// Add several items so we can verify pre-allocation.
	for i := range 5 {
		s.AddToHistory(testEvent(int32(i)))
	}
	s.AddSigningCertificate(&backend.SigningCertificate{Certificate: []byte("cert")})
	addSig(t, s, &backend.HistorySignature{Signature: []byte("sig")})

	req, err := s.GetSaveRequest("actor1")
	require.NoError(t, err)

	// The slice should have been pre-allocated to exactly fit without
	// triggering append growth beyond the initial capacity.
	// We expect: 5 history + 1 sigcert + 1 signature + 1 customStatus + 1 metadata = 9
	assert.Len(t, req.Operations, 9)
}

func TestGetPurgeRequest(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	s.AddToInbox(testEvent(0))
	s.AddToHistory(testEvent(0))
	s.AddToHistory(testEvent(1))
	s.AddSigningCertificate(&backend.SigningCertificate{Certificate: []byte("cert")})
	addSig(t, s, &backend.HistorySignature{Signature: []byte("sig1")})
	addSig(t, s, &backend.HistorySignature{Signature: []byte("sig2")})

	req, err := s.GetPurgeRequest("actor1")
	require.NoError(t, err)

	deleteKeys := make(map[string]bool)
	for _, op := range req.Operations {
		if op.Operation == api.Delete {
			if d, ok := op.Request.(api.TransactionalDelete); ok {
				deleteKeys[d.Key] = true
			}
		}
	}

	assert.True(t, deleteKeys["inbox-000000"])
	assert.True(t, deleteKeys["history-000000"])
	assert.True(t, deleteKeys["history-000001"])
	assert.True(t, deleteKeys["sigcert-000000"])
	assert.True(t, deleteKeys["signature-000000"])
	assert.True(t, deleteKeys["signature-000001"])
	assert.True(t, deleteKeys["customStatus"])
	assert.True(t, deleteKeys["metadata"])

	// Total: 1 inbox + 2 history + 1 sigcert + 2 sig + customStatus + metadata = 8
	assert.Len(t, req.Operations, 8)
}

func TestGetSaveRequest_MetadataIncludesSigningLengths(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())

	s.AddToHistory(testEvent(0))
	s.AddSigningCertificate(&backend.SigningCertificate{Certificate: []byte("cert")})
	addSig(t, s, &backend.HistorySignature{Signature: []byte("sig")})

	req, err := s.GetSaveRequest("actor1")
	require.NoError(t, err)

	// Find and unmarshal the metadata operation.
	var metadataBytes []byte
	for _, op := range req.Operations {
		if op.Operation == api.Upsert {
			if u, ok := op.Request.(api.TransactionalUpsert); ok {
				if u.Key == "metadata" {
					metadataBytes, _ = u.Value.([]byte)
					break
				}
			}
		}
	}

	require.NotEmpty(t, metadataBytes)

	var meta backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(metadataBytes, &meta))

	assert.Equal(t, uint64(1), meta.GetHistoryLength())
	assert.Equal(t, uint64(1), meta.GetSigningCertificateLength())
	assert.Equal(t, uint64(1), meta.GetSignatureLength())
}

func TestGetChunkedSaveRequest_NoChunkingWhenUnderLimit(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())
	s.AddToHistory(testEvent(1))
	s.AddToHistory(testEvent(2))

	chunks, err := s.GetChunkedSaveRequest("actor1", 0)
	require.NoError(t, err)
	assert.Len(t, chunks, 1, "maxChunkSize <= 0 should never chunk")

	chunks, err = s.GetChunkedSaveRequest("actor1", 1_000)
	require.NoError(t, err)
	assert.Len(t, chunks, 1, "ops under limit should fit in one chunk")
}

func TestGetChunkedSaveRequest_MetadataAlwaysLast(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())
	for i := int32(0); i < 25; i++ {
		s.AddToHistory(testEvent(i))
	}

	chunks, err := s.GetChunkedSaveRequest("actor1", 10)
	require.NoError(t, err)
	require.Greater(t, len(chunks), 1, "25 history adds + metadata should require chunking with limit=10")

	// metadata must live in the final chunk, as its last operation.
	last := chunks[len(chunks)-1]
	require.NotEmpty(t, last.Operations)
	tail := last.Operations[len(last.Operations)-1]
	up, ok := tail.Request.(api.TransactionalUpsert)
	require.True(t, ok, "final op should be an Upsert")
	assert.Equal(t, metadataKey, up.Key, "final op must be the metadata write")

	// No earlier chunk may contain metadata or customStatus.
	for i, c := range chunks[:len(chunks)-1] {
		for _, op := range c.Operations {
			if u, ok := op.Request.(api.TransactionalUpsert); ok {
				assert.NotEqual(t, metadataKey, u.Key, "chunk %d must not contain metadata", i)
				assert.NotEqual(t, customStatusKey, u.Key, "chunk %d must not contain customStatus", i)
			}
		}
	}

	// Every chunk must respect the size limit.
	for i, c := range chunks {
		assert.LessOrEqual(t, len(c.Operations), 10, "chunk %d exceeds limit", i)
	}
}

func TestGetChunkedSaveRequest_FinalChunkTooLarge(t *testing.T) {
	t.Parallel()

	s := NewState(testOpts())
	// Simulate a ContinueAsNew / purge: many history entries, no adds. This
	// produces a pile of deletes that must all fit with metadata in the
	// final chunk.
	for i := int32(0); i < 20; i++ {
		s.AddToHistory(testEvent(i))
	}
	req1, err := s.GetSaveRequest("a")
	require.NoError(t, err)
	_ = req1
	s.Reset() // pushes 20 deletes into the next save

	_, err = s.GetChunkedSaveRequest("a", 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DeleteWithPrefix", "error should point user to DeleteWithPrefix for purge")
}

// storeFromBytes builds a GetBulk response from a keyed byte map. Missing keys
// are returned absent from the map, matching real state-store semantics.
func storeFromBytes(entries map[string][]byte) func(ctx context.Context, req *api.GetBulkStateRequest, lock bool) (api.BulkStateResponse, error) {
	return func(ctx context.Context, req *api.GetBulkStateRequest, lock bool) (api.BulkStateResponse, error) {
		resp := api.BulkStateResponse{}
		for _, k := range req.Keys {
			if v, ok := entries[k]; ok {
				resp[k] = v
			}
		}
		return resp, nil
	}
}

func TestLoadWorkflowState_EmptyStateIsOneRoundTrip(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	fake := statefake.New().WithGetBulkFn(func(ctx context.Context, req *api.GetBulkStateRequest, lock bool) (api.BulkStateResponse, error) {
		calls.Add(1)
		// Metadata key absent ⇒ no workflow state.
		return api.BulkStateResponse{}, nil
	})

	ws, err := LoadWorkflowState(t.Context(), fake, "no-such-id", testOpts())
	require.NoError(t, err)
	assert.Nil(t, ws)
	assert.Equal(t, int32(1), calls.Load(), "empty state should cost one GetBulk round trip")
}

func TestLoadWorkflowState_SmallHistoryIsOneRoundTrip(t *testing.T) {
	t.Parallel()

	// 5 history events, 3 inbox, no signing — all comfortably under
	// speculativeReadHint.
	metaBytes, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
		Generation:    1,
		InboxLength:   3,
		HistoryLength: 5,
	})
	require.NoError(t, err)

	entries := map[string][]byte{metadataKey: metaBytes}
	for i := uint64(0); i < 5; i++ {
		b, err := proto.Marshal(testEvent(int32(i)))
		require.NoError(t, err)
		entries[getMultiEntryKeyName(historyKeyPrefix, i)] = b
	}
	for i := uint64(0); i < 3; i++ {
		b, err := proto.Marshal(testEvent(int32(100 + i)))
		require.NoError(t, err)
		entries[getMultiEntryKeyName(inboxKeyPrefix, i)] = b
	}

	var calls atomic.Int32
	fake := statefake.New().WithGetBulkFn(func(ctx context.Context, req *api.GetBulkStateRequest, lock bool) (api.BulkStateResponse, error) {
		calls.Add(1)
		return storeFromBytes(entries)(ctx, req, lock)
	})

	ws, err := LoadWorkflowState(t.Context(), fake, "wf-1", testOpts())
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Len(t, ws.History, 5)
	assert.Len(t, ws.Inbox, 3)
	assert.Equal(t, int32(1), calls.Load(), "history fitting under speculativeReadHint should cost one GetBulk")
}

func TestLoadWorkflowState_LongHistoryTriggersTailFetch(t *testing.T) {
	t.Parallel()

	const longHistory = speculativeReadHint + 10 // 42 events
	metaBytes, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
		Generation:    1,
		HistoryLength: longHistory,
	})
	require.NoError(t, err)

	entries := map[string][]byte{metadataKey: metaBytes}
	for i := uint64(0); i < longHistory; i++ {
		b, err := proto.Marshal(testEvent(int32(i)))
		require.NoError(t, err)
		entries[getMultiEntryKeyName(historyKeyPrefix, i)] = b
	}

	var calls atomic.Int32
	var secondCallKeys []string
	fake := statefake.New().WithGetBulkFn(func(ctx context.Context, req *api.GetBulkStateRequest, lock bool) (api.BulkStateResponse, error) {
		n := calls.Add(1)
		if n == 2 {
			secondCallKeys = append([]string(nil), req.Keys...)
		}
		return storeFromBytes(entries)(ctx, req, lock)
	})

	ws, err := LoadWorkflowState(t.Context(), fake, "wf-long", testOpts())
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Len(t, ws.History, int(longHistory))
	assert.Equal(t, int32(2), calls.Load(), "history exceeding speculativeReadHint should cost a tail GetBulk")
	// Tail request must only fetch the 10 missing indices, not refetch the
	// speculated ones — otherwise we haven't saved any work.
	assert.Len(t, secondCallKeys, 10)
	for i, k := range secondCallKeys {
		want := getMultiEntryKeyName(historyKeyPrefix, uint64(speculativeReadHint+i))
		assert.Equal(t, want, k, fmt.Sprintf("tail key %d", i))
	}
}
