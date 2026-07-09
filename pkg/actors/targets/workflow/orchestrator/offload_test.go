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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wfbackenderrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/payloadstore"
	payloadstorefake "github.com/dapr/dapr/pkg/runtime/wfengine/payloadstore/fake"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// newOffloadTestOrchestrator builds an orchestrator whose actor state
// applies transactional operations to kv, the way a state store would.
func newOffloadTestOrchestrator(t *testing.T, store payloadstore.Store, kv map[string][]byte) *orchestrator {
	t.Helper()

	actorState := statefake.New().
		WithTransactionalStateOperationFn(func(_ context.Context, _ bool, req *actorapi.TransactionalRequest, _ bool) error {
			for _, op := range req.Operations {
				switch typed := op.Request.(type) {
				case actorapi.TransactionalUpsert:
					data, ok := typed.Value.([]byte)
					require.True(t, ok)
					kv[typed.Key] = data
				case actorapi.TransactionalDelete:
					delete(kv, typed.Key)
				default:
					t.Fatalf("unexpected operation type %T", op.Request)
				}
			}
			return nil
		}).
		WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			return &actorapi.StateResponse{Data: kv[req.Key]}, nil
		})

	actors := fake.New().WithState(func(context.Context) (actorstate.Interface, error) {
		return actorState, nil
	})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            actors,
		PayloadStore:      store,
	})
	require.NoError(t, err)

	return fact.GetOrCreate("offload-test-wf").(*orchestrator)
}

func offloadTestState(payload string) *wfenginestate.State {
	s := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	s.AddToHistory(&backend.HistoryEvent{
		EventId:   1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 1,
				Result:          wrapperspb.String(payload),
			},
		},
	})
	return s
}

func persistedResult(t *testing.T, kv map[string][]byte) string {
	t.Helper()
	data, ok := kv["history-000000"]
	require.True(t, ok, "history event was not persisted")
	var persisted backend.HistoryEvent
	require.NoError(t, proto.Unmarshal(data, &persisted))
	return persisted.GetTaskCompleted().GetResult().GetValue()
}

func Test_signAndSaveState_offloadsLargePayloads(t *testing.T) {
	t.Parallel()

	const payload = "a payload larger than the threshold"

	kv := make(map[string][]byte)
	store := payloadstorefake.New().WithThreshold(8)
	o := newOffloadTestOrchestrator(t, store, kv)

	require.NoError(t, o.signAndSaveState(t.Context(), offloadTestState(payload)))

	v := persistedResult(t, kv)
	require.True(t, payloadstore.IsReference(v), "persisted bytes must carry a reference")

	ref, err := payloadstore.DecodeReference(v)
	require.NoError(t, err)
	data, err := store.Get(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, payload, string(data))
}

func Test_signAndSaveState_putFailureIsRecoverableAndPersistsNothing(t *testing.T) {
	t.Parallel()

	errPut := errors.New("store unavailable")

	kv := make(map[string][]byte)
	store := payloadstorefake.New().
		WithThreshold(8).
		WithPutFn(func(context.Context, string, []byte) (payloadstore.Reference, error) {
			return payloadstore.Reference{}, errPut
		})
	o := newOffloadTestOrchestrator(t, store, kv)

	err := o.signAndSaveState(t.Context(), offloadTestState("a payload larger than the threshold"))
	require.ErrorIs(t, err, errPut)
	assert.True(t, wfbackenderrors.IsRecoverable(err),
		"a Put failure must fail the turn recoverably so the reminder retries it")
	assert.Empty(t, kv, "nothing may be persisted when offload fails")
	assert.Nil(t, o.state, "cached state must be invalidated so the retry reloads from the store")
}

func Test_signAndSaveState_nilStoreLeavesPayloadInline(t *testing.T) {
	t.Parallel()

	const payload = "a payload larger than the threshold"

	kv := make(map[string][]byte)
	o := newOffloadTestOrchestrator(t, nil, kv)

	require.NoError(t, o.signAndSaveState(t.Context(), offloadTestState(payload)))

	v := persistedResult(t, kv)
	assert.False(t, payloadstore.IsReference(v))
	assert.Equal(t, payload, v, "with no store configured the payload must persist inline, unchanged")
}
