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

package encryption_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	contribcrypto "github.com/dapr/components-contrib/crypto"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption/encryptiontest"
	"github.com/dapr/durabletask-go/api/protos"
)

const secret = "4111-1111-1111-1111"

type counting struct {
	contribcrypto.SubtleCrypto
	unwraps atomic.Int32
}

func (c *counting) UnwrapKey(ctx context.Context, wrapped []byte, alg, keyName string, nonce, tag, aad []byte) (jwk.Key, error) {
	c.unwraps.Add(1)
	return c.SubtleCrypto.UnwrapKey(ctx, wrapped, alg, keyName, nonce, tag, aad)
}

func countUnwraps(t *testing.T, env *encryptiontest.Env) *counting {
	t.Helper()
	p, ok := env.Store.GetCryptoProvider(encryptiontest.Component)
	require.True(t, ok)
	c := &counting{SubtleCrypto: p}
	env.Store.AddCryptoProvider(encryptiontest.Component, c)
	return c
}

func TestInstanceKey(t *testing.T) {
	t.Parallel()
	k := encryptiontest.NewKey(t)

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()
		sealed := k.Seal("aad", []byte(secret))
		assert.NotContains(t, string(sealed), secret)
		pt, err := k.Unseal("aad", sealed)
		require.NoError(t, err)
		assert.Equal(t, secret, string(pt))
	})

	t.Run("empty plaintext", func(t *testing.T) {
		t.Parallel()
		pt, err := k.Unseal("aad", k.Seal("aad", nil))
		require.NoError(t, err)
		assert.Empty(t, pt)
	})

	t.Run("wrong aad fails", func(t *testing.T) {
		t.Parallel()
		_, err := k.Unseal("other", k.Seal("aad", []byte(secret)))
		require.Error(t, err)
	})

	t.Run("modified fails", func(t *testing.T) {
		t.Parallel()
		sealed := k.Seal("aad", []byte(secret))
		sealed[len(sealed)/2] ^= 1
		_, err := k.Unseal("aad", sealed)
		require.Error(t, err)
	})

	t.Run("truncated fails", func(t *testing.T) {
		t.Parallel()
		_, err := k.Unseal("aad", k.Seal("aad", []byte(secret))[:20])
		require.Error(t, err)
	})

	t.Run("other key fails", func(t *testing.T) {
		t.Parallel()
		_, err := encryptiontest.NewKey(t).Unseal("aad", k.Seal("aad", []byte(secret)))
		require.Error(t, err)
	})

	t.Run("string", func(t *testing.T) {
		t.Parallel()
		s := k.SealString("aad", secret)
		assert.True(t, strings.HasPrefix(s, encryption.Prefix))
		got, err := k.UnsealString("aad", s)
		require.NoError(t, err)
		assert.Equal(t, secret, got)

		_, err = k.UnsealString("aad", secret)
		require.ErrorIs(t, err, encryption.ErrNotSealed)
	})
}

func TestEncryptor(t *testing.T) {
	t.Parallel()

	t.Run("a fresh encryptor opens a key another one wrapped", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		k, err := env.Encryptor(t, encryptiontest.Key).NewInstanceKey(t.Context())
		require.NoError(t, err)

		w := k.Wrapped()
		assert.Equal(t, encryptiontest.Component+"/"+encryptiontest.Key, w.GetKid())
		assert.Equal(t, "A256KW", w.GetAlg())
		assert.NotEmpty(t, w.GetWrapped())

		opened, err := env.Encryptor(t, encryptiontest.Key).Open(t.Context(), w)
		require.NoError(t, err)
		pt, err := opened.Unseal("aad", k.Seal("aad", []byte(secret)))
		require.NoError(t, err)
		assert.Equal(t, secret, string(pt))
	})

	t.Run("open caches the unwrapped key", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		k, err := env.Encryptor(t, encryptiontest.Key).NewInstanceKey(t.Context())
		require.NoError(t, err)

		c := countUnwraps(t, env)
		enc := env.Encryptor(t, encryptiontest.Key)
		for range 3 {
			_, err = enc.Open(t.Context(), k.Wrapped())
			require.NoError(t, err)
		}
		assert.Equal(t, int32(1), c.unwraps.Load())
	})

	t.Run("the encryptor that wrapped a key never unwraps it", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		c := countUnwraps(t, env)
		enc := env.Encryptor(t, encryptiontest.Key)
		k, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		_, err = enc.Open(t.Context(), k.Wrapped())
		require.NoError(t, err)
		assert.Zero(t, c.unwraps.Load())
	})

	t.Run("rewrap keeps the key under a new kek", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		k, err := env.Encryptor(t, encryptiontest.Key).NewInstanceKey(t.Context())
		require.NoError(t, err)

		env.AddKey(t, "kek2.json")
		enc2 := env.Encryptor(t, "kek2.json")
		assert.True(t, k.NeedsRewrap(enc2.CurrentKID()))

		rk, err := enc2.Rewrap(t.Context(), k)
		require.NoError(t, err)
		assert.Equal(t, encryptiontest.Component+"/kek2.json", rk.Wrapped().GetKid())
		assert.False(t, rk.NeedsRewrap(enc2.CurrentKID()))

		opened, err := env.Encryptor(t, "kek2.json").Open(t.Context(), rk.Wrapped())
		require.NoError(t, err)
		pt, err := opened.Unseal("aad", k.Seal("aad", []byte(secret)))
		require.NoError(t, err)
		assert.Equal(t, secret, string(pt))

		// Keys wrapped under the old KEK stay readable.
		_, err = enc2.Open(t.Context(), k.Wrapped())
		require.NoError(t, err)
	})

	t.Run("missing component", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		env.Store.DeleteCryptoProvider(encryptiontest.Component)
		_, err := env.Encryptor(t, encryptiontest.Key).NewInstanceKey(t.Context())
		require.ErrorContains(t, err, "crypto component 'kek-store' not found")
	})

	t.Run("malformed kid", func(t *testing.T) {
		t.Parallel()
		_, err := encryptiontest.NewEncryptor(t).Open(t.Context(), &internalsv1pb.WorkflowEncryptionKey{Kid: "nokey"})
		require.ErrorContains(t, err, "malformed key id")
	})

	t.Run("unsupported algorithm", func(t *testing.T) {
		t.Parallel()
		_, err := encryption.New(encryption.Options{ComponentName: "c", KeyName: "k", WrapAlgorithm: "A128CBC"})
		require.ErrorContains(t, err, "unsupported key wrap algorithm")
	})

	t.Run("self check", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, encryptiontest.NewEncryptor(t).SelfCheck(t.Context()))
	})

	t.Run("self check with a missing key", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		require.Error(t, env.Encryptor(t, "missing.json").SelfCheck(t.Context()))
	})

	t.Run("concurrent open", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		enc := env.Encryptor(t, encryptiontest.Key)
		keys := make([]*internalsv1pb.WorkflowEncryptionKey, 8)
		for i := range keys {
			k, err := enc.NewInstanceKey(t.Context())
			require.NoError(t, err)
			keys[i] = k.Wrapped()
		}
		opener := env.Encryptor(t, encryptiontest.Key)
		var wg sync.WaitGroup
		for i := range 64 {
			wg.Go(func() {
				_, err := opener.Open(t.Context(), keys[i%len(keys)])
				assert.NoError(t, err)
			})
		}
		wg.Wait()
	})
}

// payloadEvents returns one event of every type that carries a payload, each
// carrying secret in every payload field and in no other field.
func payloadEvents() []*protos.HistoryEvent {
	sv := wrapperspb.String
	fd := &protos.TaskFailureDetails{
		ErrorType:    "MyError",
		ErrorMessage: secret,
		StackTrace:   sv(secret),
		InnerFailure: &protos.TaskFailureDetails{ErrorType: "Inner", ErrorMessage: secret, StackTrace: sv(secret)},
	}
	ts := timestamppb.Now()
	ev := func(id int32, e any) *protos.HistoryEvent {
		h := &protos.HistoryEvent{EventId: id, Timestamp: ts}
		switch v := e.(type) {
		case *protos.HistoryEvent_ExecutionStarted:
			h.EventType = v
		case *protos.HistoryEvent_ExecutionCompleted:
			h.EventType = v
		case *protos.HistoryEvent_ExecutionTerminated:
			h.EventType = v
		case *protos.HistoryEvent_TaskScheduled:
			h.EventType = v
		case *protos.HistoryEvent_TaskCompleted:
			h.EventType = v
		case *protos.HistoryEvent_TaskFailed:
			h.EventType = v
		case *protos.HistoryEvent_ChildWorkflowInstanceCreated:
			h.EventType = v
		case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
			h.EventType = v
		case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
			h.EventType = v
		case *protos.HistoryEvent_EventSent:
			h.EventType = v
		case *protos.HistoryEvent_EventRaised:
			h.EventType = v
		case *protos.HistoryEvent_ContinueAsNew:
			h.EventType = v
		case *protos.HistoryEvent_ExecutionSuspended:
			h.EventType = v
		case *protos.HistoryEvent_ExecutionResumed:
			h.EventType = v
		}
		return h
	}
	return []*protos.HistoryEvent{
		ev(-1, &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{
			Name: "wf", Version: sv("v1"), Input: sv(secret),
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "id", ExecutionId: sv("exec")},
			ParentInstance:   &protos.ParentInstanceInfo{TaskScheduledId: 3, Name: sv("parent")},
		}}),
		ev(-1, &protos.HistoryEvent_ExecutionCompleted{ExecutionCompleted: &protos.ExecutionCompletedEvent{Result: sv(secret), FailureDetails: fd}}),
		ev(-1, &protos.HistoryEvent_ExecutionTerminated{ExecutionTerminated: &protos.ExecutionTerminatedEvent{Input: sv(secret)}}),
		ev(1, &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "act", Input: sv(secret), TaskExecutionId: "te"}}),
		ev(-1, &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 1, Result: sv(secret)}}),
		ev(-1, &protos.HistoryEvent_TaskFailed{TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: 1, FailureDetails: fd}}),
		ev(2, &protos.HistoryEvent_ChildWorkflowInstanceCreated{ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{InstanceId: "child", Name: "cwf", Input: sv(secret)}}),
		ev(-1, &protos.HistoryEvent_ChildWorkflowInstanceCompleted{ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{TaskScheduledId: 2, Result: sv(secret)}}),
		ev(-1, &protos.HistoryEvent_ChildWorkflowInstanceFailed{ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{TaskScheduledId: 2, FailureDetails: fd}}),
		ev(3, &protos.HistoryEvent_EventSent{EventSent: &protos.EventSentEvent{InstanceId: "other", Name: "ev", Input: sv(secret)}}),
		ev(-1, &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "ev", Input: sv(secret)}}),
		ev(-1, &protos.HistoryEvent_ContinueAsNew{ContinueAsNew: &protos.ContinueAsNewEvent{Input: sv(secret)}}),
		ev(-1, &protos.HistoryEvent_ExecutionSuspended{ExecutionSuspended: &protos.ExecutionSuspendedEvent{Input: sv(secret)}}),
		ev(-1, &protos.HistoryEvent_ExecutionResumed{ExecutionResumed: &protos.ExecutionResumedEvent{Input: sv(secret)}}),
	}
}

func TestSealMessage(t *testing.T) {
	t.Parallel()
	k := encryptiontest.NewKey(t)

	for _, e := range payloadEvents() {
		t.Run(e.ProtoReflect().WhichOneof(e.ProtoReflect().Descriptor().Oneofs().ByName("eventType")).TextName(), func(t *testing.T) {
			t.Parallel()
			orig := proto.Clone(e)

			sealed, err := encryption.SealMessage(k, "row", e)
			require.NoError(t, err)
			assert.True(t, proto.Equal(orig, e), "input must not be modified")

			b, err := proto.Marshal(sealed)
			require.NoError(t, err)
			assert.NotContains(t, string(b), secret)
			assert.Contains(t, string(b), encryption.Prefix)

			// Structure stays readable.
			assert.Equal(t, e.GetEventId(), sealed.GetEventId())
			assert.True(t, proto.Equal(e.GetTimestamp(), sealed.GetTimestamp()))
			assert.Equal(t, e.GetExecutionStarted().GetName(), sealed.GetExecutionStarted().GetName())
			assert.Equal(t, e.GetExecutionStarted().GetVersion().GetValue(), sealed.GetExecutionStarted().GetVersion().GetValue())
			assert.Equal(t, e.GetExecutionStarted().GetWorkflowInstance().GetExecutionId().GetValue(), sealed.GetExecutionStarted().GetWorkflowInstance().GetExecutionId().GetValue())
			assert.Equal(t, e.GetTaskScheduled().GetName(), sealed.GetTaskScheduled().GetName())
			assert.Equal(t, e.GetTaskFailed().GetFailureDetails().GetErrorType(), sealed.GetTaskFailed().GetFailureDetails().GetErrorType())

			require.NoError(t, encryption.UnsealMessage(k, "row", sealed))
			assert.True(t, proto.Equal(orig, sealed))
		})
	}

	t.Run("a value moved to another row fails", func(t *testing.T) {
		t.Parallel()
		sealed, err := encryption.SealMessage(k, "history-000001", payloadEvents()[0])
		require.NoError(t, err)
		require.Error(t, encryption.UnsealMessage(k, "history-000002", sealed))
	})

	t.Run("a value moved to another field fails", func(t *testing.T) {
		t.Parallel()
		sealed, err := encryption.SealMessage(k, "row", payloadEvents()[1])
		require.NoError(t, err)
		c := sealed.GetExecutionCompleted()
		c.Result, c.FailureDetails.StackTrace = c.GetFailureDetails().GetStackTrace(), c.GetResult()
		require.Error(t, encryption.UnsealMessage(k, "row", sealed))
	})

	t.Run("a plaintext payload in a sealed message fails", func(t *testing.T) {
		t.Parallel()
		sealed, err := encryption.SealMessage(k, "row", payloadEvents()[0])
		require.NoError(t, err)
		sealed.GetExecutionStarted().Input = wrapperspb.String(secret)
		require.ErrorIs(t, encryption.UnsealMessage(k, "row", sealed), encryption.ErrNotSealed)
	})

	t.Run("propagated history", func(t *testing.T) {
		t.Parallel()
		raw, err := proto.Marshal(payloadEvents()[0])
		require.NoError(t, err)
		ph := &protos.PropagatedHistory{Chunks: []*protos.PropagatedHistoryChunk{{
			AppId: "app", InstanceId: "id", WorkflowName: "wf",
			RawEvents:     [][]byte{raw, raw},
			RawSignatures: [][]byte{[]byte("sig")},
		}}}
		orig := proto.Clone(ph)

		sealed, err := encryption.SealMessage(k, "propagated-history", ph)
		require.NoError(t, err)
		b, err := proto.Marshal(sealed)
		require.NoError(t, err)
		assert.NotContains(t, string(b), secret)
		assert.Equal(t, [][]byte{[]byte("sig")}, sealed.GetChunks()[0].GetRawSignatures())
		assert.Equal(t, "wf", sealed.GetChunks()[0].GetWorkflowName())

		require.NoError(t, encryption.UnsealMessage(k, "propagated-history", sealed))
		assert.True(t, proto.Equal(orig, sealed))
	})
}

func TestReminder(t *testing.T) {
	t.Parallel()
	env := encryptiontest.NewEnv(t)
	enc := env.Encryptor(t, encryptiontest.Key)
	k, err := enc.NewInstanceKey(t.Context())
	require.NoError(t, err)

	inner, err := anypb.New(payloadEvents()[3])
	require.NoError(t, err)
	aad := encryption.ReminderAAD("type", "id", "run-activity")

	sealed, err := encryption.SealReminder(k, aad, inner)
	require.NoError(t, err)
	assert.True(t, encryption.IsSealedReminder(sealed))
	assert.NotContains(t, string(sealed.GetValue()), secret)

	t.Run("open with another encryptor", func(t *testing.T) {
		t.Parallel()
		got, gotKey, err := env.Encryptor(t, encryptiontest.Key).OpenReminder(t.Context(), aad, sealed)
		require.NoError(t, err)
		require.NotNil(t, gotKey)
		assert.True(t, proto.Equal(inner, got))
		assert.True(t, proto.Equal(k.Wrapped(), gotKey.Wrapped()))
	})

	t.Run("another reminder's aad fails", func(t *testing.T) {
		t.Parallel()
		_, _, err := enc.OpenReminder(t.Context(), encryption.ReminderAAD("type", "other", "run-activity"), sealed)
		require.Error(t, err)
	})

	t.Run("plaintext passes through", func(t *testing.T) {
		t.Parallel()
		got, gotKey, err := enc.OpenReminder(t.Context(), aad, inner)
		require.NoError(t, err)
		assert.Nil(t, gotKey)
		assert.Same(t, inner, got)
	})

	t.Run("nil encryptor rejects sealed data", func(t *testing.T) {
		t.Parallel()
		var none *encryption.Encryptor
		_, _, err := none.OpenReminder(t.Context(), aad, sealed)
		require.ErrorContains(t, err, "not configured")

		got, _, err := none.OpenReminder(t.Context(), aad, inner)
		require.NoError(t, err)
		assert.Same(t, inner, got)
	})
}
