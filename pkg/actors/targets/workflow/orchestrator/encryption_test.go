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

package orchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption/encryptiontest"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func TestEnsureEncryptionKey(t *testing.T) {
	t.Parallel()

	newState := func() *wfenginestate.State {
		return wfenginestate.NewState(wfenginestate.Options{WorkflowActorType: "workflow"})
	}

	t.Run("no encryptor leaves the instance in plaintext", func(t *testing.T) {
		t.Parallel()
		o := &orchestrator{factory: &factory{}, actorID: "wf"}
		st := newState()
		require.NoError(t, o.ensureEncryptionKey(t.Context(), st))
		assert.Nil(t, st.EncryptionKey)
	})

	t.Run("mints the key once", func(t *testing.T) {
		t.Parallel()
		o := &orchestrator{factory: &factory{encryptor: encryptiontest.NewEncryptor(t)}, actorID: "wf"}
		st := newState()
		require.NoError(t, o.ensureEncryptionKey(t.Context(), st))
		k := st.EncryptionKey
		require.NotNil(t, k)

		require.NoError(t, o.ensureEncryptionKey(t.Context(), st))
		assert.Same(t, k, st.EncryptionKey)
	})

	t.Run("a stored key that was not opened is never replaced", func(t *testing.T) {
		t.Parallel()
		o := &orchestrator{factory: &factory{encryptor: encryptiontest.NewEncryptor(t)}, actorID: "wf"}
		st := unopenedState(t)
		require.ErrorContains(t, o.ensureEncryptionKey(t.Context(), st), "refusing to replace it")
		assert.Nil(t, st.EncryptionKey)
	})

	t.Run("an unreachable key encryption key fails the save", func(t *testing.T) {
		t.Parallel()
		env := encryptiontest.NewEnv(t)
		o := &orchestrator{factory: &factory{encryptor: env.Encryptor(t, encryptiontest.Key)}, actorID: "wf"}
		env.Store.DeleteCryptoProvider(encryptiontest.Component)
		st := newState()
		require.Error(t, o.ensureEncryptionKey(t.Context(), st))
		assert.Nil(t, st.EncryptionKey)
	})
}

// encryptingOrchestrator returns an orchestrator with payload encryption on
// whose created reminders are captured.
func encryptingOrchestrator(t *testing.T, enc *encryption.Encryptor) (*orchestrator, func() []*actorapi.CreateReminderRequest) {
	t.Helper()
	var (
		mu      sync.Mutex
		creates []*actorapi.CreateReminderRequest
	)
	rems := remindersfake.New().WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
		mu.Lock()
		defer mu.Unlock()
		creates = append(creates, req)
		return nil
	})
	fact, err := New(t.Context(), Options{
		AppID:              "testapp",
		Namespace:          "default",
		WorkflowActorType:  "dapr.internal.default.testapp.workflow",
		ActivityActorType:  "dapr.internal.default.testapp.activity",
		RetentionActorType: "dapr.internal.default.testapp.retention",
		ActorTypeBuilder:   common.NewActorTypeBuilder("default"),
		Actors: fake.New().WithReminders(func(context.Context) (reminders.Interface, error) {
			return rems, nil
		}),
		Encryptor: enc,
	})
	require.NoError(t, err)
	return fact.GetOrCreate("wf").(*orchestrator), func() []*actorapi.CreateReminderRequest {
		mu.Lock()
		defer mu.Unlock()
		return creates
	}
}

func openCreated(t *testing.T, enc *encryption.Encryptor, req *actorapi.CreateReminderRequest) *anypb.Any {
	t.Helper()
	require.True(t, encryption.IsSealedReminder(req.Data), "reminder %s is not sealed", req.Name)
	assert.NotContains(t, string(req.Data.GetValue()), "4111")
	inner, key, err := enc.OpenReminder(t.Context(), encryption.ReminderAAD(req.ActorType, req.ActorID, req.Name), req.Data)
	require.NoError(t, err)
	require.NotNil(t, key)
	return inner
}

func TestEncryptedReminders(t *testing.T) {
	t.Parallel()

	t.Run("cascade terminate is sealed with the parent's key", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		o, created := encryptingOrchestrator(t, enc)
		key, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)

		require.NoError(t, o.createCascadeTerminateReminder(t.Context(), childRef{instanceID: "child-1"},
			&protos.ExecutionTerminatedEvent{Input: wrapperspb.String("4111-1111")}, key))
		require.Len(t, created(), 1)

		var ev backend.HistoryEvent
		require.NoError(t, openCreated(t, enc, created()[0]).UnmarshalTo(&ev))
		assert.Equal(t, "4111-1111", ev.GetExecutionTerminated().GetInput().GetValue())
	})

	t.Run("a failure reminder is sealed with the instance key", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		o, created := encryptingOrchestrator(t, enc)
		key, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)
		o.state = wfenginestate.NewState(wfenginestate.Options{WorkflowActorType: o.actorType})
		o.state.SetEncryptionKey(o.actorID, key)

		require.NoError(t, o.failTaskViaReminder(t.Context(), &protos.HistoryEvent{EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{FailureDetails: &protos.TaskFailureDetails{ErrorMessage: "4111-1111"}},
		}}))
		require.Len(t, created(), 1)

		var ev backend.HistoryEvent
		require.NoError(t, openCreated(t, enc, created()[0]).UnmarshalTo(&ev))
		assert.Equal(t, "4111-1111", ev.GetTaskFailed().GetFailureDetails().GetErrorMessage())
	})

	t.Run("a plaintext instance's reminders stay plaintext", func(t *testing.T) {
		t.Parallel()
		o, created := encryptingOrchestrator(t, encryptiontest.NewEncryptor(t))
		require.NoError(t, o.createCascadeTerminateReminder(t.Context(), childRef{instanceID: "child-1"},
			&protos.ExecutionTerminatedEvent{}, nil))
		require.Len(t, created(), 1)
		assert.False(t, encryption.IsSealedReminder(created()[0].Data))
	})

	t.Run("a payload sealed for another actor is retried", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		o, _ := encryptingOrchestrator(t, enc)
		key, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)

		inner, err := anypb.New(&protos.HistoryEvent{})
		require.NoError(t, err)
		data, err := encryption.SealReminder(key, encryption.ReminderAAD(o.actorType, "another-wf", "activity-result-x"), inner)
		require.NoError(t, err)

		require.Error(t, o.handleReminder(t.Context(), &actorapi.Reminder{
			ActorType: o.actorType, ActorID: o.actorID, Name: "activity-result-x", Data: data,
		}))
	})

	t.Run("a sealed payload is retried while encryption is not configured", func(t *testing.T) {
		t.Parallel()
		enc := encryptiontest.NewEncryptor(t)
		o, _ := encryptingOrchestrator(t, nil)
		key, err := enc.NewInstanceKey(t.Context())
		require.NoError(t, err)

		inner, err := anypb.New(&protos.HistoryEvent{})
		require.NoError(t, err)
		data, err := encryption.SealReminder(key, encryption.ReminderAAD(o.actorType, o.actorID, "activity-result-x"), inner)
		require.NoError(t, err)

		require.ErrorContains(t, o.handleReminder(t.Context(), &actorapi.Reminder{
			ActorType: o.actorType, ActorID: o.actorID, Name: "activity-result-x", Data: data,
		}), "not configured")
	})
}

// unopenedState loads an encrypted instance whose last history event is the
// tamper marker, so its key is left unopened.
func unopenedState(t *testing.T) *wfenginestate.State {
	t.Helper()
	enc := encryptiontest.NewEncryptor(t)
	key, err := enc.NewInstanceKey(t.Context())
	require.NoError(t, err)
	st := wfenginestate.NewState(wfenginestate.Options{WorkflowActorType: "workflow"})
	st.AddToHistory(&protos.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_ExecutionCompleted{ExecutionCompleted: &protos.ExecutionCompletedEvent{
		WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
		FailureDetails: &protos.TaskFailureDetails{ErrorType: wferrors.ErrorTypeHistoryTampered},
	}}})
	st.SetEncryptionKey("wf", key)
	req, err := st.GetSaveRequest("wf")
	require.NoError(t, err)

	rows := map[string][]byte{}
	for _, op := range req.Operations {
		if u, ok := op.Request.(actorapi.TransactionalUpsert); ok {
			rows[u.Key] = u.Value.([]byte)
		}
	}
	etag := "1"
	store := statefake.New().
		WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			return &actorapi.StateResponse{Data: rows[req.Key], ETag: &etag}, nil
		}).
		WithGetBulkFn(func(_ context.Context, req *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			out := actorapi.BulkStateResponse{}
			for _, k := range req.Keys {
				if v, ok := rows[k]; ok {
					out[k] = actorapi.BulkStateEntry{Data: v, ETag: &etag}
				}
			}
			return out, nil
		})
	loaded, err := wfenginestate.LoadWorkflowState(t.Context(), store, "wf", wfenginestate.Options{WorkflowActorType: "workflow", Encryptor: enc})
	require.NoError(t, err)
	require.True(t, loaded.EncryptionKeyUnopened())
	return loaded
}
