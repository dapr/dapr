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

package activity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption"
	"github.com/dapr/dapr/pkg/runtime/wfengine/encryption/encryptiontest"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

const actSecret = "4111-1111-1111-1111"

func TestInvokeKey(t *testing.T) {
	t.Parallel()
	enc := encryptiontest.NewEncryptor(t)
	key, err := enc.NewInstanceKey(t.Context())
	require.NoError(t, err)
	header, err := key.Header()
	require.NoError(t, err)

	withHeader := internalsv1pb.NewInternalInvokeRequest(todo.ExecuteActivityMethod).
		WithMetadata(map[string][]string{todo.MetadataWorkflowEncryptionKey: {header}})
	without := internalsv1pb.NewInternalInvokeRequest(todo.ExecuteActivityMethod)

	t.Run("opens the workflow's key", func(t *testing.T) {
		t.Parallel()
		a := &activity{factory: &factory{encryptor: enc}}
		got, err := a.invokeKey(t.Context(), withHeader)
		require.NoError(t, err)
		assert.True(t, proto.Equal(key.Wrapped(), got.Wrapped()))
	})

	t.Run("refuses an encrypted workflow without encryption configured", func(t *testing.T) {
		t.Parallel()
		a := &activity{factory: &factory{}}
		_, err := a.invokeKey(t.Context(), withHeader)
		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	})

	t.Run("plaintext without encryption configured", func(t *testing.T) {
		t.Parallel()
		a := &activity{factory: &factory{}}
		got, err := a.invokeKey(t.Context(), without)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("a key wrapped by another key encryption key fails", func(t *testing.T) {
		t.Parallel()
		a := &activity{factory: &factory{encryptor: encryptiontest.NewEncryptor(t)}}
		_, err := a.invokeKey(t.Context(), withHeader)
		require.Error(t, err)
	})
}

func TestEncryptedActivityReminders(t *testing.T) {
	t.Parallel()
	enc := encryptiontest.NewEncryptor(t)
	key, err := enc.NewInstanceKey(t.Context())
	require.NoError(t, err)

	sched := &captureScheduler{}
	f := &factory{
		actorType:         "dapr.internal.default.testapp.activity",
		workflowActorType: "dapr.internal.default.testapp.workflow",
		reminders:         sched,
		encryptor:         enc,
	}

	invocation := &protos.ActivityInvocation{HistoryEvent: &protos.HistoryEvent{
		EventId:   1,
		EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "act", Input: wrapperspb.String(actSecret)}},
	}}
	require.NoError(t, f.createActivityReminder(t.Context(), "wf::1::0", invocation, time.Now(), nil, key))

	result := &backend.HistoryEvent{
		EventId:   -1,
		EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 1, Result: wrapperspb.String(actSecret)}},
	}
	require.NoError(t, f.createWorkflowResultReminder(t.Context(), f.workflowActorType, "wf", "exec-1", result, key))

	sched.mu.Lock()
	creates := sched.creates
	sched.mu.Unlock()
	require.Len(t, creates, 2)

	for _, req := range creates {
		require.True(t, encryption.IsSealedReminder(req.Data), "reminder %s is not sealed", req.Name)
		assert.NotContains(t, string(req.Data.GetValue()), actSecret)
	}

	t.Run("both reminders open with the instance key", func(t *testing.T) {
		t.Parallel()
		run, runKey, err := enc.OpenReminder(t.Context(), encryption.ReminderAAD(creates[0].ActorType, creates[0].ActorID, creates[0].Name), creates[0].Data)
		require.NoError(t, err)
		assert.True(t, proto.Equal(key.Wrapped(), runKey.Wrapped()))
		var gotInvocation protos.ActivityInvocation
		require.NoError(t, run.UnmarshalTo(&gotInvocation))
		assert.True(t, proto.Equal(invocation, &gotInvocation))

		res, resKey, err := enc.OpenReminder(t.Context(), encryption.ReminderAAD(creates[1].ActorType, creates[1].ActorID, creates[1].Name), creates[1].Data)
		require.NoError(t, err)
		assert.True(t, proto.Equal(key.Wrapped(), resKey.Wrapped()))
		var gotResult backend.HistoryEvent
		require.NoError(t, res.UnmarshalTo(&gotResult))
		assert.True(t, proto.Equal(result, &gotResult))
	})

	t.Run("a run reminder sealed for another activity is retried", func(t *testing.T) {
		t.Parallel()
		a := &activity{factory: f, actorID: "wf::2::0"}
		require.Error(t, a.handleReminder(t.Context(), &actorapi.Reminder{
			ActorType: f.actorType, ActorID: "wf::2::0", Name: activityReminderName, Data: creates[0].Data,
		}))
	})

	t.Run("a sealed run reminder is retried while encryption is not configured", func(t *testing.T) {
		t.Parallel()
		a := &activity{factory: &factory{actorType: f.actorType}, actorID: "wf::1::0"}
		require.ErrorContains(t, a.handleReminder(t.Context(), &actorapi.Reminder{
			ActorType: f.actorType, ActorID: "wf::1::0", Name: activityReminderName, Data: creates[0].Data,
		}), "not configured")
	})
}

// TestPlaintextWorkflowActivityReminders covers a workflow that sent no key:
// its payloads are stored in plaintext, so the activity's reminders are too,
// and a workflow app without encryption can read the result reminder.
func TestPlaintextWorkflowActivityReminders(t *testing.T) {
	t.Parallel()
	sched := &captureScheduler{}
	f := &factory{
		actorType:         "dapr.internal.default.testapp.activity",
		workflowActorType: "dapr.internal.default.testapp.workflow",
		reminders:         sched,
		encryptor:         encryptiontest.NewEncryptor(t),
	}
	require.NoError(t, f.createActivityReminder(t.Context(), "wf::1::0", &protos.ActivityInvocation{HistoryEvent: &protos.HistoryEvent{}}, time.Now(), nil, nil))
	require.NoError(t, f.createWorkflowResultReminder(t.Context(), "dapr.internal.default.otherapp.workflow", "wf", "", &backend.HistoryEvent{}, nil))

	sched.mu.Lock()
	defer sched.mu.Unlock()
	require.Len(t, sched.creates, 2)
	for _, req := range sched.creates {
		assert.False(t, encryption.IsSealedReminder(req.Data), "reminder %s", req.Name)
	}
}
