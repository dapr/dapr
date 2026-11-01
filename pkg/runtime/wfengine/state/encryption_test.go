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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption/encryptiontest"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

const (
	encSecret  = "4111-1111-1111-1111"
	encActorID = "wf-1"
)

var encTS = timestamppb.New(time.Unix(1_700_000_000, 0))

// memStore is an actor state store keeping rows in a map.
type memStore struct {
	rows map[string][]byte
}

func newMemStore() *memStore {
	return &memStore{rows: map[string][]byte{}}
}

func (m *memStore) Interface() actorstate.Interface {
	etag := "1"
	entry := func(k string) (api.BulkStateEntry, bool) {
		v, ok := m.rows[k]
		if !ok {
			return api.BulkStateEntry{}, false
		}
		return api.BulkStateEntry{Data: v, ETag: &etag}, true
	}
	return statefake.New().
		WithGetFn(func(_ context.Context, req *api.GetStateRequest, _ bool) (*api.StateResponse, error) {
			e, _ := entry(req.Key)
			return &api.StateResponse{Data: e.Data, ETag: e.ETag}, nil
		}).
		WithGetBulkFn(func(_ context.Context, req *api.GetBulkStateRequest, _ bool) (api.BulkStateResponse, error) {
			out := api.BulkStateResponse{}
			for _, k := range req.Keys {
				if e, ok := entry(k); ok {
					out[k] = e
				}
			}
			return out, nil
		})
}

func (m *memStore) apply(t testing.TB, req *api.TransactionalRequest) {
	t.Helper()
	for _, op := range req.Operations {
		switch r := op.Request.(type) {
		case api.TransactionalUpsert:
			m.rows[r.Key] = r.Value.([]byte)
		case api.TransactionalDelete:
			delete(m.rows, r.Key)
		}
	}
}

func (m *memStore) save(t *testing.T, s *State) {
	t.Helper()
	req, err := s.GetSaveRequest(encActorID)
	require.NoError(t, err)
	m.apply(t, req)
	s.ResetChangeTracking()
}

func (m *memStore) load(t *testing.T, opts Options) (*State, error) {
	t.Helper()
	return LoadWorkflowState(t.Context(), m.Interface(), encActorID, opts)
}

func (m *memStore) requireNoSecret(t *testing.T) {
	t.Helper()
	for k, v := range m.rows {
		assert.NotContains(t, string(v), encSecret, "row %s holds the plaintext", k)
	}
}

func encOpts(enc *encryption.Encryptor) Options {
	o := testOpts()
	o.Encryptor = enc
	return o
}

func payloadHistory() []*backend.HistoryEvent {
	ts := encTS
	return []*backend.HistoryEvent{
		{EventId: -1, Timestamp: ts, EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{
			Name: "wf", Input: wrapperspb.String(encSecret),
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: encActorID},
		}}},
		{EventId: 0, Timestamp: ts, EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{
			Name: "act", Input: wrapperspb.String(encSecret),
		}}},
		{EventId: -1, Timestamp: ts, EventType: &protos.HistoryEvent_TaskFailed{TaskFailed: &protos.TaskFailedEvent{
			TaskScheduledId: 0,
			FailureDetails:  &protos.TaskFailureDetails{ErrorType: "Declined", ErrorMessage: encSecret, StackTrace: wrapperspb.String(encSecret)},
		}}},
	}
}

func raisedEvent() *backend.HistoryEvent {
	return &backend.HistoryEvent{EventId: -1, Timestamp: encTS, EventType: &protos.HistoryEvent_EventRaised{
		EventRaised: &protos.EventRaisedEvent{Name: "approve", Input: wrapperspb.String(encSecret)},
	}}
}

// fullState returns a new state carrying the secret in every payload row.
func fullState(t *testing.T, opts Options) *State {
	t.Helper()
	s := NewState(opts)
	for _, e := range payloadHistory() {
		s.AddToHistory(e)
	}
	s.AddToInbox(raisedEvent())
	s.CustomStatus = wrapperspb.String(encSecret)
	s.customStatusChanged = true
	s.setCreationInput(wrapperspb.String(encSecret), &protos.ParentInstanceInfo{WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent"}})
	raw, err := proto.Marshal(raisedEvent())
	require.NoError(t, err)
	s.SetIncomingHistory(&protos.PropagatedHistory{Chunks: []*protos.PropagatedHistoryChunk{{AppId: "caller", RawEvents: [][]byte{raw}}}})
	return s
}

func assertPlaintext(t *testing.T, want, got *State) {
	t.Helper()
	require.Len(t, got.History, len(want.History))
	for i := range want.History {
		assert.True(t, proto.Equal(want.History[i], got.History[i]), "history %d", i)
	}
	require.Len(t, got.Inbox, len(want.Inbox))
	for i := range want.Inbox {
		assert.True(t, proto.Equal(want.Inbox[i], got.Inbox[i]), "inbox %d", i)
	}
	assert.Equal(t, want.CustomStatus.GetValue(), got.CustomStatus.GetValue())
	assert.Equal(t, want.CreationInput.GetValue(), got.CreationInput.GetValue())
	assert.True(t, proto.Equal(want.IncomingHistory, got.IncomingHistory))
}

func storedKey(t *testing.T, m *memStore) *internalsv1pb.WorkflowEncryptionKey {
	t.Helper()
	var w internalsv1pb.WorkflowEncryptionKey
	require.Contains(t, m.rows, encryptionKey)
	require.NoError(t, proto.Unmarshal(m.rows[encryptionKey], &w))
	return &w
}

func TestEncryptedState(t *testing.T) {
	t.Parallel()

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		m := newMemStore()

		s := fullState(t, encOpts(enc))
		k, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		m.requireNoSecret(t)
		assert.Equal(t, uint64(0), storedKey(t, m).GetHistorySealedFrom())

		// Rows stay readable protobuf with their structure intact.
		var e backend.HistoryEvent
		require.NoError(t, proto.Unmarshal(m.rows["history-000001"], &e))
		assert.Equal(t, "act", e.GetTaskScheduled().GetName())
		assert.True(t, strings.HasPrefix(e.GetTaskScheduled().GetInput().GetValue(), encryption.Prefix))
		require.NoError(t, proto.Unmarshal(m.rows["history-000002"], &e))
		assert.Equal(t, "Declined", e.GetTaskFailed().GetFailureDetails().GetErrorType())

		got, err := m.load(t, encOpts(enc))
		require.NoError(t, err)
		assertPlaintext(t, fullState(t, testOpts()), got)
		assert.False(t, got.EncryptionRewrapNeeded())
	})

	t.Run("another sidecar opens it", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		m := newMemStore()
		s := fullState(t, testOpts())
		k, err := env.Encryptor(t, encryptiontest.Key).NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		got, err := m.load(t, encOpts(env.Encryptor(t, encryptiontest.Key)))
		require.NoError(t, err)
		assertPlaintext(t, fullState(t, testOpts()), got)
	})

	t.Run("the key row is written once", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.AddToHistory(payloadHistory()[0])
		s.SetEncryptionKey(encActorID, encryptiontest.NewKey(t))
		m := newMemStore()
		m.save(t, s)
		require.Contains(t, m.rows, encryptionKey)

		s.AddToHistory(payloadHistory()[1])
		req, err := s.GetSaveRequest(encActorID)
		require.NoError(t, err)
		for _, op := range req.Operations {
			if u, ok := op.Request.(api.TransactionalUpsert); ok {
				assert.NotEqual(t, encryptionKey, u.Key)
			}
		}
	})

	t.Run("cannot be loaded without encryption configured", func(t *testing.T) {
		t.Parallel()
		m := newMemStore()
		s := fullState(t, testOpts())
		s.SetEncryptionKey(encActorID, encryptiontest.NewKey(t))
		m.save(t, s)

		_, err := m.load(t, testOpts())
		var cerr *wferrors.ConfigurationError
		require.ErrorAs(t, err, &cerr)
	})

	t.Run("an unavailable key is retried, not treated as tampering", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		m := newMemStore()
		s := fullState(t, testOpts())
		k, err := env.Encryptor(t, encryptiontest.Key).NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		other := encryptiontest.NewEnv(t)
		_, err = m.load(t, encOpts(other.Encryptor(t, encryptiontest.Key)))
		require.Error(t, err)
		var verr *wferrors.VerificationError
		var cerr *wferrors.ConfigurationError
		assert.NotErrorAs(t, err, &verr)
		assert.NotErrorAs(t, err, &cerr)
	})

	t.Run("a modified payload fails verification", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		m := newMemStore()
		s := fullState(t, testOpts())
		k, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		var e backend.HistoryEvent
		require.NoError(t, proto.Unmarshal(m.rows["history-000001"], &e))
		in := e.GetTaskScheduled().GetInput()
		in.Value = in.GetValue()[:len(in.GetValue())-2] + "AA"
		m.rows["history-000001"], err = proto.Marshal(&e)
		require.NoError(t, err)

		_, err = m.load(t, encOpts(enc))
		var verr *wferrors.VerificationError
		require.ErrorAs(t, err, &verr)
	})

	t.Run("a plaintext payload in an encrypted instance fails verification", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		m := newMemStore()
		s := fullState(t, testOpts())
		k, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		m.rows["history-000001"], err = proto.Marshal(payloadHistory()[1])
		require.NoError(t, err)

		_, err = m.load(t, encOpts(enc))
		var verr *wferrors.VerificationError
		require.ErrorAs(t, err, &verr)
	})

	t.Run("rows moved to another instance fail verification", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		m := newMemStore()
		s := fullState(t, testOpts())
		k, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		_, err = LoadWorkflowState(t.Context(), m.Interface(), "wf-other", encOpts(enc))
		var verr *wferrors.VerificationError
		require.ErrorAs(t, err, &verr)
	})

	t.Run("rows swapped within the instance fail verification", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		m := newMemStore()
		s := NewState(testOpts())
		s.AddToHistory(payloadHistory()[1])
		s.AddToHistory(payloadHistory()[1])
		k, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		m.rows["history-000000"], m.rows["history-000001"] = m.rows["history-000001"], m.rows["history-000000"]
		_, err = m.load(t, encOpts(enc))
		var verr *wferrors.VerificationError
		require.ErrorAs(t, err, &verr)
	})

	t.Run("an unsigned plaintext instance is rewritten sealed", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		m := newMemStore()
		m.save(t, fullState(t, testOpts()))
		require.NotContains(t, m.rows, encryptionKey)

		s, err := m.load(t, encOpts(enc))
		require.NoError(t, err)
		assertPlaintext(t, fullState(t, testOpts()), s)

		k, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		m.requireNoSecret(t)
		assert.Equal(t, uint64(0), storedKey(t, m).GetHistorySealedFrom())
		got, err := m.load(t, encOpts(enc))
		require.NoError(t, err)
		assertPlaintext(t, fullState(t, testOpts()), got)
	})

	t.Run("a signed plaintext instance keeps its signed rows", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		for _, e := range payloadHistory() {
			s.AddToHistory(e)
		}
		addSig(t, s, &backend.HistorySignature{StartEventIndex: 0, EventCount: 3})
		s.ResetChangeTracking()
		s.loaded = true

		s.AddToHistory(raisedEvent())
		s.SetEncryptionKey(encActorID, encryptiontest.NewKey(t))
		assert.Equal(t, uint64(3), s.historySealedFrom)

		req, err := s.GetSaveRequest(encActorID)
		require.NoError(t, err)
		upserts := map[string][]byte{}
		for _, op := range req.Operations {
			if u, ok := op.Request.(api.TransactionalUpsert); ok {
				upserts[u.Key] = u.Value.([]byte)
			}
		}
		for _, k := range []string{"history-000000", "history-000001", "history-000002"} {
			assert.NotContains(t, upserts, k, "signed row %s must not be rewritten", k)
		}
		require.Contains(t, upserts, "history-000003")
		assert.NotContains(t, string(upserts["history-000003"]), encSecret)

		var w internalsv1pb.WorkflowEncryptionKey
		require.NoError(t, proto.Unmarshal(upserts[encryptionKey], &w))
		assert.Equal(t, uint64(3), w.GetHistorySealedFrom())

		// Loading unseals only from the recorded index.
		loaded := NewState(testOpts())
		for i := range 3 {
			loaded.History = append(loaded.History, payloadHistory()[i])
		}
		var e backend.HistoryEvent
		require.NoError(t, proto.Unmarshal(upserts["history-000003"], &e))
		loaded.History = append(loaded.History, &e)
		loaded.EncryptionKey, loaded.aadBase, loaded.historySealedFrom = s.EncryptionKey, s.aadBase, 3
		require.NoError(t, loaded.unsealAll())
		assert.True(t, proto.Equal(raisedEvent().GetEventRaised(), loaded.History[3].GetEventRaised()))
	})

	t.Run("continue as new seals the new history from the start", func(t *testing.T) {
		t.Parallel()
		s := NewState(testOpts())
		s.SetEncryptionKey(encActorID, encryptiontest.NewKey(t))
		s.historySealedFrom = 3
		s.ResetChangeTracking()

		s.ApplyRuntimeStateChanges(&backend.WorkflowRuntimeState{ContinuedAsNew: true, NewEvents: payloadHistory()[:1]})
		assert.Equal(t, uint64(0), s.historySealedFrom)
		req, err := s.GetSaveRequest(encActorID)
		require.NoError(t, err)
		var found bool
		for _, op := range req.Operations {
			if u, ok := op.Request.(api.TransactionalUpsert); ok && u.Key == encryptionKey {
				found = true
			}
		}
		assert.True(t, found, "the key row must record the new sealed-from index")
	})

	t.Run("the tamper marker stays readable", func(t *testing.T) {
		t.Parallel()
		m := newMemStore()
		s := NewState(testOpts())
		s.AddToHistory(payloadHistory()[0])
		s.AddToHistory(tamperMarkerEvent())
		s.SetEncryptionKey(encActorID, encryptiontest.NewKey(t))
		m.save(t, s)

		var e backend.HistoryEvent
		require.NoError(t, proto.Unmarshal(m.rows["history-000001"], &e))
		assert.True(t, IsTamperMarker(&e))
		assert.Equal(t, "boom", e.GetExecutionCompleted().GetFailureDetails().GetErrorMessage())
	})

	t.Run("a tamper-marked instance keeps its key until reset", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		m := newMemStore()
		s := NewState(testOpts())
		s.AddToHistory(payloadHistory()[0])
		s.AddToHistory(tamperMarkerEvent())
		k, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		got, err := m.load(t, encOpts(enc))
		require.NoError(t, err)
		require.True(t, got.EncryptionKeyUnopened())

		// A recreate discards the rows the key sealed, and the key with them.
		got.Reset()
		assert.False(t, got.EncryptionKeyUnopened())
		m.save(t, got)
		assert.NotContains(t, m.rows, encryptionKey)
	})

	t.Run("purge deletes the key row", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		m := newMemStore()
		s := fullState(t, testOpts())
		k, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		got, err := m.load(t, encOpts(enc))
		require.NoError(t, err)
		req, err := got.GetPurgeRequest(encActorID)
		require.NoError(t, err)
		m.apply(t, req)
		assert.NotContains(t, m.rows, encryptionKey)
	})

	t.Run("a key wrapped under another key encryption key is re-wrapped", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		m := newMemStore()
		s := fullState(t, testOpts())
		k, err := env.Encryptor(t, encryptiontest.Key).NewInstanceKey(t.Context())
		require.NoError(t, err)
		s.SetEncryptionKey(encActorID, k)
		m.save(t, s)

		env.AddKey(t, "kek2.json")
		enc2 := env.Encryptor(t, "kek2.json")
		got, err := m.load(t, encOpts(enc2))
		require.NoError(t, err)
		assert.True(t, got.EncryptionRewrapNeeded())

		rk, err := enc2.Rewrap(t.Context(), got.EncryptionKey)
		require.NoError(t, err)
		got.SetEncryptionKey(encActorID, rk)
		m.save(t, got)
		assert.Equal(t, enc2.CurrentKID(), storedKey(t, m).GetKid())

		again, err := m.load(t, encOpts(enc2))
		require.NoError(t, err)
		assert.False(t, again.EncryptionRewrapNeeded())
		assertPlaintext(t, fullState(t, testOpts()), again)
	})
}

// BenchmarkEncryptedState measures saving and loading 1,000 history events
// with a 256 byte payload each, in plaintext and sealed.
func BenchmarkEncryptedState(b *testing.B) {
	payload := strings.Repeat("x", 256)
	for _, encrypted := range []bool{false, true} {
		b.Run(fmt.Sprintf("encrypted=%t", encrypted), func(b *testing.B) {
			enc := encryptiontest.NewEncryptor(b)
			opts := encOpts(enc)
			s := NewState(opts)
			for i := range 1000 {
				s.AddToHistory(&backend.HistoryEvent{EventId: int32(i), Timestamp: encTS, EventType: &protos.HistoryEvent_TaskScheduled{
					TaskScheduled: &protos.TaskScheduledEvent{Name: "act", Input: wrapperspb.String(payload)},
				}})
			}
			if encrypted {
				k, err := enc.NewInstanceKey(b.Context())
				require.NoError(b, err)
				s.SetEncryptionKey(encActorID, k)
			}

			m := newMemStore()
			req, err := s.GetSaveRequest(encActorID)
			require.NoError(b, err)
			m.apply(b, req)

			b.Run("save", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.GetSaveRequest(encActorID); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("load", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := LoadWorkflowState(b.Context(), m.Interface(), encActorID, opts); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
