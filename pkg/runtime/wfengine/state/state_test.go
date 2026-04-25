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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
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

func tamperMarkerEvent() *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{
				WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
				FailureDetails: &protos.TaskFailureDetails{
					ErrorType:    wferrors.ErrorTypeHistoryTampered,
					ErrorMessage: "boom",
				},
			},
		},
	}
}

func TestIsTamperMarker(t *testing.T) {
	t.Parallel()

	t.Run("tamper marker", func(t *testing.T) {
		t.Parallel()
		assert.True(t, IsTamperMarker(tamperMarkerEvent()))
	})

	t.Run("non-tamper failed completion", func(t *testing.T) {
		t.Parallel()
		e := &backend.HistoryEvent{
			EventType: &protos.HistoryEvent_ExecutionCompleted{
				ExecutionCompleted: &protos.ExecutionCompletedEvent{
					WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
					FailureDetails: &protos.TaskFailureDetails{ErrorType: "user-failure"},
				},
			},
		}
		assert.False(t, IsTamperMarker(e))
	})

	t.Run("completed without failure details", func(t *testing.T) {
		t.Parallel()
		e := &backend.HistoryEvent{
			EventType: &protos.HistoryEvent_ExecutionCompleted{
				ExecutionCompleted: &protos.ExecutionCompletedEvent{
					WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
				},
			},
		}
		assert.False(t, IsTamperMarker(e))
	})

	t.Run("non-completion event", func(t *testing.T) {
		t.Parallel()
		assert.False(t, IsTamperMarker(testEvent(0)))
	})
}

func TestMarkAsTamperFailed_AppendsMarkerAndPersists(t *testing.T) {
	t.Parallel()

	prior := NewState(testOpts())
	prior.AddToHistory(testEvent(0))
	prior.AddToHistory(testEvent(1))
	prior.ResetChangeTracking()

	var saved *api.TransactionalRequest
	store := statefake.New().WithTransactionalStateOperationFn(
		func(_ context.Context, _ bool, req *api.TransactionalRequest, _ bool) error {
			saved = req
			return nil
		},
	)

	got, err := MarkAsTamperFailed(t.Context(), store, "wf-1", testOpts(), prior, errors.New("chain broken"))
	require.NoError(t, err)
	require.NotNil(t, got)

	require.Len(t, got.History, 3, "marker should be appended to existing history")
	last := got.History[len(got.History)-1]
	assert.True(t, IsTamperMarker(last))
	assert.Equal(t, "chain broken", last.GetExecutionCompleted().GetFailureDetails().GetErrorMessage())

	// First two events must be unchanged.
	assert.Equal(t, int32(0), got.History[0].GetEventId())
	assert.Equal(t, int32(1), got.History[1].GetEventId())

	// No new signatures or certs are written — the marker is intentionally unsigned.
	assert.Empty(t, got.Signatures)
	assert.Empty(t, got.SigningCertificates)

	// Persistence happened: an upsert at history-000002 must be present.
	require.NotNil(t, saved)
	var foundMarkerUpsert bool
	for _, op := range saved.Operations {
		if op.Operation != api.Upsert {
			continue
		}
		u, ok := op.Request.(api.TransactionalUpsert)
		if !ok {
			continue
		}
		if u.Key == "history-000002" {
			foundMarkerUpsert = true
		}
	}
	assert.True(t, foundMarkerUpsert, "expected upsert for the appended marker history key")
}

func TestMarkAsTamperFailed_Idempotent(t *testing.T) {
	t.Parallel()

	prior := NewState(testOpts())
	prior.AddToHistory(testEvent(0))
	prior.AddToHistory(tamperMarkerEvent())
	prior.ResetChangeTracking()

	var writes int
	store := statefake.New().WithTransactionalStateOperationFn(
		func(_ context.Context, _ bool, _ *api.TransactionalRequest, _ bool) error {
			writes++
			return nil
		},
	)

	got, err := MarkAsTamperFailed(t.Context(), store, "wf-1", testOpts(), prior, errors.New("ignored"))
	require.NoError(t, err)
	assert.Same(t, prior, got)
	assert.Len(t, got.History, 2, "history must not grow when marker already present")
	assert.Equal(t, 0, writes, "no store write should happen when already marked")
}

func TestMarkAsTamperFailed_NilPriorCreatesFreshState(t *testing.T) {
	t.Parallel()

	store := statefake.New()

	got, err := MarkAsTamperFailed(t.Context(), store, "wf-1", testOpts(), nil, errors.New("no prior"))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.History, 1)
	assert.True(t, IsTamperMarker(got.History[0]))
}

// TestLoadWorkflowState_TamperMarkerBypassesConfigurationError exercises
// the bypass added so previously tamper-marked workflows remain loadable
// after a host config change (e.g. signing was disabled). The signing
// enforcement checks must short-circuit when the loaded history ends in
// a tamper marker so the FAILED status still surfaces.
func TestLoadWorkflowState_TamperMarkerBypassesConfigurationError(t *testing.T) {
	t.Parallel()

	const actorID = "wf-tampered"

	// Build a state with a signature plus a tamper marker as the last
	// history event, then persist it through a fake store.
	store := statefake.New()
	bulk := map[string][]byte{}
	store = store.
		WithGetFn(func(_ context.Context, req *api.GetStateRequest, _ bool) (*api.StateResponse, error) {
			if v, ok := bulk[req.Key]; ok {
				return &api.StateResponse{Data: v}, nil
			}
			return &api.StateResponse{}, nil
		}).
		WithGetBulkFn(func(_ context.Context, req *api.GetBulkStateRequest, _ bool) (api.BulkStateResponse, error) {
			out := api.BulkStateResponse{}
			for _, k := range req.Keys {
				if v, ok := bulk[k]; ok {
					out[k] = v
				}
			}
			return out, nil
		}).
		WithTransactionalStateOperationFn(func(_ context.Context, _ bool, req *api.TransactionalRequest, _ bool) error {
			for _, op := range req.Operations {
				if op.Operation == api.Upsert {
					u := op.Request.(api.TransactionalUpsert)
					bulk[u.Key] = u.Value.([]byte)
				}
			}
			return nil
		})

	histEvt := testEvent(0)
	histBytes, err := proto.Marshal(histEvt)
	require.NoError(t, err)
	bulk["history-000000"] = histBytes

	markerBytes, err := proto.Marshal(tamperMarkerEvent())
	require.NoError(t, err)
	bulk["history-000001"] = markerBytes

	sig := &backend.HistorySignature{StartEventIndex: 0, EventCount: 1, Signature: []byte("sig")}
	sigBytes, err := proto.Marshal(sig)
	require.NoError(t, err)
	bulk["signature-000000"] = sigBytes

	cert := &backend.SigningCertificate{Certificate: []byte("cert")}
	certBytes, err := proto.Marshal(cert)
	require.NoError(t, err)
	bulk["sigcert-000000"] = certBytes

	metaBytes, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{
		HistoryLength:            2,
		SignatureLength:          1,
		SigningCertificateLength: 1,
		Generation:               1,
	})
	require.NoError(t, err)
	bulk["metadata"] = metaBytes

	// Load with no signer / no app ID — the configuration that would
	// normally yield a ConfigurationError. The tamper marker must
	// short-circuit the check so the load succeeds.
	got, err := LoadWorkflowState(t.Context(), store, actorID, Options{
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.History, 2)
	assert.True(t, IsTamperMarker(got.History[1]))
}
