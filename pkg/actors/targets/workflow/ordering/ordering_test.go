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

package ordering

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	"github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/durabletask-go/api/protos"
)

// harness is one key over an in-memory row that honours ETags.
type harness struct {
	t        *testing.T
	row      []byte
	version  int
	released []*actorapi.CreateReminderRequest
	// failCreate fails reminder creates.
	failCreate bool
	key        *ordering
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t}
	st := statefake.New().
		WithGetFn(func(_ context.Context, _ *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			return &actorapi.StateResponse{Data: h.row, ETag: new(strconv.Itoa(h.version))}, nil
		}).
		WithTransactionalStateOperationFn(func(_ context.Context, _ bool, req *actorapi.TransactionalRequest, _ bool) error {
			switch r := req.Operations[0].Request.(type) {
			case actorapi.TransactionalUpsert:
				require.Equal(t, strconv.Itoa(h.version), *r.ETag)
				h.row = r.Value.([]byte)
			case actorapi.TransactionalDelete:
				h.row = nil
			}
			h.version++
			return nil
		})
	rem := remindersfake.New().WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
		if h.failCreate {
			return errors.New("scheduler unavailable")
		}
		h.released = append(h.released, req)
		return nil
	})
	f, err := New(t.Context(), Options{
		Actors: actorsfake.New().
			WithState(func(context.Context) (state.Interface, error) { return st, nil }).
			WithReminders(func(context.Context) (reminders.Interface, error) { return rem, nil }),
		WorkflowActorType: "wf",
		ActorType:         "ordering",
	})
	require.NoError(t, err)
	h.key = f.GetOrCreate("k").(*ordering)
	return h
}

func (h *harness) invoke(method string, payload any) (*internalsv1pb.InternalInvokeResponse, error) {
	data, err := json.Marshal(payload)
	require.NoError(h.t, err)
	return h.key.InvokeMethod(h.t.Context(), internalsv1pb.NewInternalInvokeRequest(method).WithData(data))
}

func (h *harness) call(method string, payload any) bool {
	res, err := h.invoke(method, payload)
	require.NoError(h.t, err)
	var existing bool
	if method == EnqueueMethod {
		require.NoError(h.t, json.Unmarshal(res.GetMessage().GetData().GetValue(), &existing))
	}
	return existing
}

// releases returns the instances released since the last call.
func (h *harness) releases() []string {
	ids := make([]string, 0, len(h.released))
	for _, r := range h.released {
		ids = append(ids, r.ActorID+"/"+r.Name)
	}
	h.released = nil
	return ids
}

func entry(id, exec string) Entry {
	return Entry{InstanceID: id, CreateExecutionID: exec, WorkflowName: "wf"}
}

func done(e Entry, o Outcome) DoneRequest { return DoneRequest{Entry: e, Outcome: o} }

func TestActor(t *testing.T) {
	h := newHarness(t)
	a, b, c := entry("a", "1"), entry("b", "1"), entry("c", "1")
	b.Policy = cancel

	assert.False(t, h.call(EnqueueMethod, a))
	assert.False(t, h.call(EnqueueMethod, b))
	assert.False(t, h.call(EnqueueMethod, c))
	assert.Equal(t, []string{"a/ordering-release-1"}, h.releases(), "only the head is released")
	assert.True(t, h.call(EnqueueMethod, a), "the head re-joins")
	assert.Equal(t, []string{"a/ordering-release-1"}, h.releases(), "a repeat re-notifies the head")

	h.call(DoneMethod, done(a, OutcomeFailed))
	assert.Equal(t, []string{"b/ordering-cancel-1"}, h.releases(), "b cancels past a's failure")
	h.call(DoneMethod, done(b, OutcomeSkipped))
	assert.Equal(t, []string{"c/ordering-release-1"}, h.releases())
	h.call(DoneMethod, done(c, OutcomeSucceeded))
	assert.Nil(t, h.row, "an idle key with no failure deletes its row")
}

// A failed reminder create after a save is repaired by the caller's retry,
// which changes nothing and notifies the head again.
func TestNotifyAfterSave(t *testing.T) {
	h := newHarness(t)
	h.call(EnqueueMethod, entry("a", "1"))
	h.call(EnqueueMethod, entry("b", "1"))
	h.releases()

	h.failCreate = true
	_, err := h.invoke(DoneMethod, done(entry("a", "1"), OutcomeSucceeded))
	require.Error(t, err)
	var q queue
	require.NoError(t, json.Unmarshal(h.row, &q))
	assert.Equal(t, "b", q.Head.InstanceID, "the save landed")

	h.failCreate = false
	h.call(DoneMethod, done(entry("a", "1"), OutcomeSucceeded))
	assert.Equal(t, []string{"b/ordering-release-1"}, h.releases())
}

// The loop runs a key's calls one at a time, so concurrent calls never race
// on the row, and it stops once the key has no calls left.
func TestConcurrentCalls(t *testing.T) {
	h := newHarness(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			_, err := h.invoke(EnqueueMethod, entry(strconv.Itoa(i), "1"))
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	var q queue
	require.NoError(t, json.Unmarshal(h.row, &q))
	assert.Len(t, q.Queue, 19)
	assert.Len(t, h.released, 1, "only the first to arrive is released")
	assert.Eventually(t, func() bool { return h.key.Len() == 0 }, time.Second, time.Millisecond)
}

func TestReminders(t *testing.T) {
	h := newHarness(t)
	due := time.Now().Add(time.Hour).UTC()
	a, b := entry("a", "1"), entry("b", "1")
	a.DueTime = &due
	b.DueTime, b.Policy = &due, cancel
	h.call(EnqueueMethod, a)
	h.call(EnqueueMethod, b)

	require.Len(t, h.released, 1)
	r := h.released[0]
	assert.Equal(t, "wf", r.ActorType)
	assert.Equal(t, due.Format(time.RFC3339Nano), r.DueTime, "a delayed start is released at its start time")
	assert.Equal(t, "wf", *r.ConcurrencyKey)
	assert.Equal(t, ReminderData{Key: "k"}, reminderData(t, r))

	h.call(DoneMethod, done(a, OutcomeFailed))
	require.Len(t, h.released, 2)
	r = h.released[1]
	assert.Equal(t, CancelReminderName("1"), r.Name)
	assert.Nil(t, r.ConcurrencyKey, "a cancel runs no workflow code")
	assert.Less(t, r.DueTime, due.Format(time.RFC3339Nano), "a cancel is due now")
	assert.Equal(t, ReminderData{Key: "k", After: "a"}, reminderData(t, r))
}

func reminderData(t *testing.T, r *actorapi.CreateReminderRequest) ReminderData {
	t.Helper()
	var rd ReminderData
	require.NoError(t, json.Unmarshal(r.Data.GetValue(), &rd))
	return rd
}

func TestValidate(t *testing.T) {
	require.NoError(t, ValidateKey("customer-orders_1"))
	require.NoError(t, ValidateKey(strings.Repeat("é", 64)), "the limit counts characters")
	for _, k := range []string{"", "a.b", "a b", strings.Repeat("k", 65)} {
		require.Error(t, ValidateKey(k), k)
	}
	require.NoError(t, ValidatePolicy(cancel))
	require.Error(t, ValidatePolicy(protos.OrderingFailurePolicy(7)))
}
