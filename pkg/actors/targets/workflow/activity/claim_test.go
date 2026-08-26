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
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/claim"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/activity/inflight"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// recordStore backs the state fake with an in-memory execution-claim record
// table keyed by activity actor ID, with first-write-wins ETag semantics so
// the conditional deletes in claim are exercised for real.
type recordStore struct {
	lock sync.Mutex
	data map[string][]byte
	etag map[string]int
}

func newRecordStore() *recordStore {
	return &recordStore{data: make(map[string][]byte), etag: make(map[string]int)}
}

func (s *recordStore) fake() *statefake.Fake {
	return statefake.New().
		WithGetFn(func(_ context.Context, req *actorsapi.GetStateRequest, _ bool) (*actorsapi.StateResponse, error) {
			s.lock.Lock()
			defer s.lock.Unlock()
			b, ok := s.data[req.ActorID]
			if !ok {
				return &actorsapi.StateResponse{}, nil
			}
			etag := strconv.Itoa(s.etag[req.ActorID])
			return &actorsapi.StateResponse{Data: b, ETag: &etag}, nil
		}).
		WithTransactionalStateOperationFn(func(_ context.Context, _ bool, req *actorsapi.TransactionalRequest, _ bool) error {
			s.lock.Lock()
			defer s.lock.Unlock()
			for _, op := range req.Operations {
				switch r := op.Request.(type) {
				case actorsapi.TransactionalUpsert:
					b, err := json.Marshal(r.Value)
					if err != nil {
						return err
					}
					s.data[req.ActorID] = b
					s.etag[req.ActorID]++
				case actorsapi.TransactionalDelete:
					if r.ETag != nil && *r.ETag != strconv.Itoa(s.etag[req.ActorID]) {
						return errors.New("etag mismatch")
					}
					delete(s.data, req.ActorID)
				}
			}
			return nil
		})
}

func (s *recordStore) get(t *testing.T, actorID string) (*claim.Record, bool) {
	t.Helper()
	s.lock.Lock()
	defer s.lock.Unlock()
	b, ok := s.data[actorID]
	if !ok {
		return nil, false
	}
	var rec claim.Record
	require.NoError(t, json.Unmarshal(b, &rec))
	return &rec, true
}

func (s *recordStore) set(t *testing.T, actorID string, rec claim.Record) {
	t.Helper()
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	s.lock.Lock()
	defer s.lock.Unlock()
	s.data[actorID] = b
	s.etag[actorID]++
}

func newClaimHarness(t *testing.T) (*factory, *recordStore, chan *backend.ActivityWorkItem) {
	t.Helper()
	store := newRecordStore()
	scheduled := make(chan *backend.ActivityWorkItem, 2)
	driveCtx, driveCancel := context.WithCancel(t.Context())
	f := &factory{
		driveCtx:          driveCtx,
		driveCancel:       driveCancel,
		appID:             "testapp",
		actorType:         "dapr.internal.default.testapp.activity",
		workflowActorType: "dapr.internal.default.testapp.workflow",
		router:            routerfake.New(),
		signing:           &signing.Signing{Namespace: "default"},
		state:             store.fake(),
		fastPath:          true,
		rootCtx:           t.Context(),
		staleClaimAfter:   time.Hour,
		claims: claim.New(claim.Options{
			ActorType:      "dapr.internal.default.testapp.activity",
			State:          store.fake(),
			HeartbeatEvery: time.Millisecond * 10,
			Retention:      time.Millisecond * 50,
			StaleAfter:     time.Hour,
		}),
		scheduler: func(_ context.Context, wi *backend.ActivityWorkItem) error {
			scheduled <- wi
			return nil
		},
	}
	return f, store, scheduled
}

func haltNothingHosted(t *testing.T, f *factory) {
	t.Helper()
	require.NoError(t, f.HaltNonHosted(t.Context(), func(*actorsapi.LookupActorRequest) bool {
		return false
	}))
}

// Test_claimGuard_lifecycle: written on churn halt, heartbeat advances,
// Completed on clean finish, deleted after retention or on error.
func Test_claimGuard_lifecycle(t *testing.T) {
	t.Parallel()

	const actorID = "wf::3"
	key := actorID + "::gen1"

	t.Run("churn halt writes, heartbeats, completes and deletes the record", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)

		call, owner := f.inflight.Acquire(key)
		require.True(t, owner)
		f.GetOrCreate(actorID)
		haltNothingHosted(t, f)

		rec, ok := store.get(t, actorID)
		require.True(t, ok, "the record must be durable before the churn halt returns (pre-unlock)")
		assert.Equal(t, key, rec.TaskKey)
		assert.False(t, rec.Completed)
		hb1 := rec.HeartbeatMs

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			rec, ok := store.get(t, actorID)
			if assert.True(c, ok) {
				assert.Greater(c, rec.HeartbeatMs, hb1, "the heartbeat must advance every period")
			}
		}, time.Second*5, time.Millisecond*5)

		call.Finish(nil)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			rec, ok := store.get(t, actorID)
			if assert.True(c, ok, "the record must be marked Completed before deletion") {
				assert.True(c, rec.Completed)
			}
		}, time.Second*5, time.Millisecond*5)

		require.Eventually(t, func() bool {
			_, ok := store.get(t, actorID)
			return !ok
		}, time.Second*5, time.Millisecond*5, "the completed record must self-delete after retention")
		f.claims.Wait()
	})

	t.Run("execution error deletes the record without completing it", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)

		call, owner := f.inflight.Acquire(key)
		require.True(t, owner)
		f.GetOrCreate(actorID)
		haltNothingHosted(t, f)

		require.Eventually(t, func() bool {
			_, ok := store.get(t, actorID)
			return ok
		}, time.Second*5, time.Millisecond*5)

		call.Finish(errors.New("app crashed"))
		require.Eventually(t, func() bool {
			_, ok := store.get(t, actorID)
			return !ok
		}, time.Second*5, time.Millisecond*5, "a failed execution must delete the record so the new owner re-executes")
		f.claims.Wait()

		rec, ok := store.get(t, actorID)
		assert.False(t, ok, "no Completed marker may survive an execution error: %+v", rec)
	})

	t.Run("halt-all spawns no guard", func(t *testing.T) {
		// HaltAll is disconnection or shutdown; a record there would only
		// stall the next owner.
		t.Parallel()
		f, store, _ := newClaimHarness(t)

		call, owner := f.inflight.Acquire(key)
		require.True(t, owner)
		f.GetOrCreate(actorID)
		require.NoError(t, f.HaltAll(t.Context()))
		f.claims.Wait()

		_, ok := store.get(t, actorID)
		assert.False(t, ok)
		call.Finish(nil)
	})

	t.Run("shutdown deletes the record best effort", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		rootCtx, rootCancel := context.WithCancel(t.Context())
		f.rootCtx = rootCtx

		call, owner := f.inflight.Acquire(key)
		require.True(t, owner)
		f.GetOrCreate(actorID)
		haltNothingHosted(t, f)

		require.Eventually(t, func() bool {
			_, ok := store.get(t, actorID)
			return ok
		}, time.Second*5, time.Millisecond*5)

		rootCancel()
		require.Eventually(t, func() bool {
			_, ok := store.get(t, actorID)
			return !ok
		}, time.Second*5, time.Millisecond*5, "a guard stopped by shutdown must not leave a record stalling the new owner")
		f.claims.Wait()
		call.Finish(nil)
	})

	t.Run("no guard for a settled claim", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)

		call, owner := f.inflight.Acquire(key)
		require.True(t, owner)
		call.Finish(nil)
		f.GetOrCreate(actorID)
		haltNothingHosted(t, f)
		f.claims.Wait()

		_, ok := store.get(t, actorID)
		assert.False(t, ok, "a settled claim needs no guard: its outcome is already delivered")
	})

	t.Run("no guard without a claim", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)

		f.GetOrCreate(actorID)
		haltNothingHosted(t, f)
		f.claims.Wait()

		_, ok := store.get(t, actorID)
		assert.False(t, ok)
	})

	t.Run("no guard when the fast path is disabled", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		f.fastPath = false

		call, owner := f.inflight.Acquire(key)
		require.True(t, owner)
		f.GetOrCreate(actorID)
		haltNothingHosted(t, f)
		f.claims.Wait()

		_, ok := store.get(t, actorID)
		assert.False(t, ok)
		call.Finish(nil)
	})

	t.Run("retention delete spares a newer generation's record", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		f.claims = claim.New(claim.Options{
			ActorType:      f.actorType,
			State:          store.fake(),
			HeartbeatEvery: time.Millisecond * 10,
			// Long enough to overwrite the row before the delete leg runs.
			Retention:  time.Second,
			StaleAfter: time.Hour,
		})

		call, owner := f.inflight.Acquire(key)
		require.True(t, owner)
		f.GetOrCreate(actorID)
		haltNothingHosted(t, f)

		call.Finish(nil)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			rec, ok := store.get(t, actorID)
			if assert.True(c, ok) {
				assert.True(c, rec.Completed)
			}
		}, time.Second*5, time.Millisecond*5)

		// A newer scheduling generation of the same actor takes the row
		// while the old guard sits out its retention.
		store.set(t, actorID, claim.Record{TaskKey: actorID + "::gen2", HeartbeatMs: time.Now().UnixMilli()})
		f.claims.Wait()

		rec, ok := store.get(t, actorID)
		require.True(t, ok, "the old guard's retention delete must not destroy the newer generation's live claim")
		assert.Equal(t, actorID+"::gen2", rec.TaskKey)
	})

	t.Run("repeated halts spawn a single guard per task key", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)

		call, owner := f.inflight.Acquire(key)
		require.True(t, owner)
		f.GetOrCreate(actorID)
		haltNothingHosted(t, f)
		f.GetOrCreate(actorID)
		haltNothingHosted(t, f)

		assert.Equal(t, 1, f.claims.Active())

		call.Finish(nil)
		require.Eventually(t, func() bool {
			_, ok := store.get(t, actorID)
			return !ok
		}, time.Second*5, time.Millisecond*5)
		f.claims.Wait()
	})
}

func Test_checkClaimRecord(t *testing.T) {
	t.Parallel()

	const actorID = "wf::3"
	key := actorID + "::gen1"

	t.Run("missing record proceeds", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newClaimHarness(t)
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Proceed, outcome)
	})

	t.Run("record for another scheduling proceeds", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		store.set(t, actorID, claim.Record{TaskKey: actorID + "::gen0", HeartbeatMs: time.Now().UnixMilli()})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Proceed, outcome)
	})

	t.Run("live heartbeat defers", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		store.set(t, actorID, claim.Record{TaskKey: key, HeartbeatMs: time.Now().UnixMilli()})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Defer, outcome)
	})

	t.Run("completed acks", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		store.set(t, actorID, claim.Record{TaskKey: key, HeartbeatMs: time.Now().Add(-time.Hour).UnixMilli(), Completed: true})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Completed, outcome)
	})

	t.Run("stale record from another scheduling is reaped on read", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		f.claims = claim.New(claim.Options{
			ActorType:  f.actorType,
			State:      store.fake(),
			StaleAfter: time.Millisecond * 50,
		})
		store.set(t, actorID, claim.Record{TaskKey: actorID + "::oldrun", HeartbeatMs: time.Now().Add(-time.Second).UnixMilli()})
		// First sighting only opens the observation window; the other
		// scheduling's record does not block this taskKey.
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Proceed, outcome)
		_, ok := store.get(t, actorID)
		assert.True(t, ok, "the record cannot read dead on a single observation")

		time.Sleep(time.Millisecond * 75)
		outcome, err = f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Proceed, outcome)
		_, ok = store.get(t, actorID)
		assert.False(t, ok, "a dead old-run record must not leak")
	})

	t.Run("live record from another scheduling is ignored but kept", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		store.set(t, actorID, claim.Record{TaskKey: actorID + "::otherrun", HeartbeatMs: time.Now().UnixMilli()})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Proceed, outcome)
		_, ok := store.get(t, actorID)
		assert.True(t, ok, "a live record may guard a newer generation and must not be deleted")
	})

	t.Run("stale heartbeat proceeds and deletes the record", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		f.claims = claim.New(claim.Options{
			ActorType:  f.actorType,
			State:      store.fake(),
			StaleAfter: time.Millisecond * 50,
		})
		store.set(t, actorID, claim.Record{TaskKey: key, HeartbeatMs: time.Now().Add(-time.Second).UnixMilli()})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Defer, outcome, "a single observation cannot read stale")

		time.Sleep(time.Millisecond * 75)
		outcome, err = f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Proceed, outcome)
		_, ok := store.get(t, actorID)
		assert.False(t, ok, "a stale record is dead weight and must be reclaimed")
	})

	t.Run("skewed past heartbeat is not insta-reclaimed", func(t *testing.T) {
		// The writer's clock ran far behind: the raw timestamp is over the
		// grace on arrival, but only reader-side observation may declare it
		// dead. A frozen value still reclaims after the grace.
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		f.claims = claim.New(claim.Options{
			ActorType:  f.actorType,
			State:      store.fake(),
			StaleAfter: time.Millisecond * 50,
		})
		store.set(t, actorID, claim.Record{TaskKey: key, HeartbeatMs: time.Now().Add(-time.Hour).UnixMilli()})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Defer, outcome, "cross-host clock skew must not reclaim a live execution")

		time.Sleep(time.Millisecond * 75)
		outcome, err = f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Proceed, outcome, "a value frozen past the grace is dead regardless of skew")
	})

	t.Run("skewed future heartbeat defers and later reclaims", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		f.claims = claim.New(claim.Options{
			ActorType:  f.actorType,
			State:      store.fake(),
			StaleAfter: time.Millisecond * 50,
		})
		store.set(t, actorID, claim.Record{TaskKey: key, HeartbeatMs: time.Now().Add(time.Hour).UnixMilli()})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Defer, outcome)

		time.Sleep(time.Millisecond * 75)
		outcome, err = f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Proceed, outcome, "a frozen future timestamp must not shield a dead guard forever")
	})

	t.Run("heartbeat change resets the observation window", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		f.claims = claim.New(claim.Options{
			ActorType:  f.actorType,
			State:      store.fake(),
			StaleAfter: time.Millisecond * 50,
		})
		store.set(t, actorID, claim.Record{TaskKey: key, HeartbeatMs: 1})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Defer, outcome)

		time.Sleep(time.Millisecond * 75)
		store.set(t, actorID, claim.Record{TaskKey: key, HeartbeatMs: 2})
		outcome, err = f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Defer, outcome, "a changed heartbeat proves the guard lives; the window must restart")

		time.Sleep(time.Millisecond * 75)
		outcome, err = f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Proceed, outcome)
	})

	t.Run("failed reclaim delete defers instead of proceeding", func(t *testing.T) {
		// A heartbeat landing between the stale read and the conditional
		// delete fails the delete: the arrival must defer, not duplicate the
		// revived execution.
		t.Parallel()
		f, _, _ := newClaimHarness(t)
		b, err := json.Marshal(claim.Record{TaskKey: key, HeartbeatMs: time.Now().Add(-time.Second).UnixMilli()})
		require.NoError(t, err)
		etag := "1"
		f.claims = claim.New(claim.Options{
			ActorType: f.actorType,
			State: statefake.New().
				WithGetFn(func(context.Context, *actorsapi.GetStateRequest, bool) (*actorsapi.StateResponse, error) {
					return &actorsapi.StateResponse{Data: b, ETag: &etag}, nil
				}).
				WithTransactionalStateOperationFn(func(context.Context, bool, *actorsapi.TransactionalRequest, bool) error {
					return errors.New("etag mismatch")
				}),
			StaleAfter: time.Millisecond * 50,
		})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Defer, outcome)

		time.Sleep(time.Millisecond * 75)
		outcome, err = f.claims.Check(t.Context(), actorID, key)
		require.NoError(t, err)
		assert.Equal(t, claim.Defer, outcome)
	})

	t.Run("read error surfaces", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newClaimHarness(t)
		f.claims = claim.New(claim.Options{
			ActorType: f.actorType,
			State: statefake.New().WithGetFn(func(context.Context, *actorsapi.GetStateRequest, bool) (*actorsapi.StateResponse, error) {
				return nil, errors.New("store down")
			}),
		})
		outcome, err := f.claims.Check(t.Context(), actorID, key)
		require.Error(t, err)
		assert.Equal(t, claim.Defer, outcome)
	})
}

// Test_executeActivity_recoveryGate: live defers, completed acks, stale
// executes.
func Test_executeActivity_recoveryGate(t *testing.T) {
	t.Parallel()

	// testInvocation carries no TaskExecutionId and no timestamp, so the
	// inflight key for actor wf::3 is the actor ID itself.
	const actorID = "wf::3"

	t.Run("live record defers with a recoverable error", func(t *testing.T) {
		t.Parallel()
		f, store, scheduled := newClaimHarness(t)
		store.set(t, actorID, claim.Record{TaskKey: actorID, HeartbeatMs: time.Now().UnixMilli()})

		a := f.GetOrCreate(actorID).(*activity)
		err := a.executeActivity(t.Context(), activityReminderName, testInvocation(), false, true)
		require.ErrorIs(t, err, claim.ErrHeldElsewhere)
		assert.True(t, wferrors.IsRecoverable(err), "the deferral must be retried, not failed terminally")
		select {
		case <-scheduled:
			t.Fatal("a deferred arrival must not dispatch a WorkItem")
		default:
		}
	})

	t.Run("completed record acks success without executing", func(t *testing.T) {
		t.Parallel()
		f, store, scheduled := newClaimHarness(t)
		store.set(t, actorID, claim.Record{TaskKey: actorID, HeartbeatMs: time.Now().Add(-time.Hour).UnixMilli(), Completed: true})

		a := f.GetOrCreate(actorID).(*activity)
		require.NoError(t, a.executeActivity(t.Context(), activityReminderName, testInvocation(), false, true))
		select {
		case <-scheduled:
			t.Fatal("a completed execution must not be re-run")
		default:
		}
	})

	t.Run("stale record executes as a fresh owner", func(t *testing.T) {
		t.Parallel()
		f, store, scheduled := newClaimHarness(t)
		f.claims = claim.New(claim.Options{
			ActorType:  f.actorType,
			State:      store.fake(),
			StaleAfter: time.Millisecond * 50,
		})
		store.set(t, actorID, claim.Record{TaskKey: actorID, HeartbeatMs: time.Now().Add(-time.Hour).UnixMilli()})

		a := f.GetOrCreate(actorID).(*activity)

		// The first arrival opens the observation window and defers; the
		// retry past the grace observes the frozen value and reclaims.
		err := a.executeActivity(t.Context(), activityReminderName, testInvocation(), false, true)
		require.ErrorIs(t, err, claim.ErrHeldElsewhere)
		time.Sleep(time.Millisecond * 75)

		ownerErr := make(chan error, 1)
		go func() {
			ownerErr <- a.executeActivity(t.Context(), activityReminderName, testInvocation(), false, true)
		}()

		var wi *backend.ActivityWorkItem
		select {
		case wi = <-scheduled:
		case <-time.After(time.Second * 5):
			t.Fatal("a stale record must not block re-execution")
		}
		wi.Result = &protos.HistoryEvent{
			EventId: -1,
			EventType: &protos.HistoryEvent_TaskCompleted{
				TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 3},
			},
		}
		callback, ok := wi.Properties[todo.CallbackChannelProperty].(chan bool)
		require.True(t, ok)
		callback <- true

		select {
		case err := <-ownerErr:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			t.Fatal("timed out waiting for the owner to finish")
		}
	})
}

func Test_gateJanitorRedispatch(t *testing.T) {
	t.Parallel()

	const actorID = "wf::3"

	t.Run("local inflight entry acks without arming a drive", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		store.set(t, actorID, claim.Record{TaskKey: actorID, HeartbeatMs: time.Now().UnixMilli()})
		call, owner := f.inflight.Acquire(actorID)
		require.True(t, owner)
		t.Cleanup(func() { call.Finish(nil) })

		a := f.GetOrCreate(actorID).(*activity)
		handled, err := a.gateJanitorRedispatch(t.Context(), testInvocation())
		require.NoError(t, err)
		assert.True(t, handled, "a local claim owns delivery; the re-dispatch must ack, not spawn a drive that could retry ungated")
	})

	t.Run("stranded local entry falls through to the rescue path", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newClaimHarness(t)
		// Stale immediately: unsettled, not held, past the (zeroed) grace.
		f.staleClaimAfter = time.Nanosecond
		f.executionHeld = func(string, int32) bool { return false }
		call, owner := f.inflight.Acquire(actorID)
		require.True(t, owner)
		t.Cleanup(func() { call.Finish(nil) })
		time.Sleep(time.Millisecond)

		a := f.GetOrCreate(actorID).(*activity)
		handled, err := a.gateJanitorRedispatch(t.Context(), testInvocation())
		require.NoError(t, err)
		assert.False(t, handled,
			"a stranded entry must not ack: acking swallows both the re-dispatch and the escalation (janitor-livelock)")
	})

	t.Run("live record defers", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		store.set(t, actorID, claim.Record{TaskKey: actorID, HeartbeatMs: time.Now().UnixMilli()})

		a := f.GetOrCreate(actorID).(*activity)
		handled, err := a.gateJanitorRedispatch(t.Context(), testInvocation())
		assert.True(t, handled)
		require.ErrorIs(t, err, claim.ErrHeldElsewhere)
	})

	t.Run("completed record acks", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		store.set(t, actorID, claim.Record{TaskKey: actorID, HeartbeatMs: 0, Completed: true})

		a := f.GetOrCreate(actorID).(*activity)
		handled, err := a.gateJanitorRedispatch(t.Context(), testInvocation())
		assert.True(t, handled)
		require.NoError(t, err)
	})

	t.Run("stale completed record is reaped while acking", func(t *testing.T) {
		t.Parallel()
		f, store, _ := newClaimHarness(t)
		f.claims = claim.New(claim.Options{
			ActorType:  f.actorType,
			State:      store.fake(),
			StaleAfter: time.Millisecond * 50,
		})
		// A restart inside the retention window leaves this row behind; the
		// guard cannot delete it again.
		store.set(t, actorID, claim.Record{TaskKey: actorID, HeartbeatMs: 12345, Completed: true})

		a := f.GetOrCreate(actorID).(*activity)

		// First read opens the reader-side observation window and acks.
		handled, err := a.gateJanitorRedispatch(t.Context(), testInvocation())
		assert.True(t, handled)
		require.NoError(t, err)
		_, ok := store.get(t, actorID)
		assert.True(t, ok, "a fresh completed row is retained for in-flight recovery arrivals")

		// A later read past the grace (but inside the 2x prune window, so the
		// observation survives) still acks and reaps the row.
		time.Sleep(75 * time.Millisecond)
		handled, err = a.gateJanitorRedispatch(t.Context(), testInvocation())
		assert.True(t, handled)
		require.NoError(t, err)
		_, ok = store.get(t, actorID)
		assert.False(t, ok, "a stale completed row must not linger forever")
	})

	t.Run("missing record proceeds", func(t *testing.T) {
		t.Parallel()
		f, _, _ := newClaimHarness(t)

		a := f.GetOrCreate(actorID).(*activity)
		handled, err := a.gateJanitorRedispatch(t.Context(), testInvocation())
		require.NoError(t, err)
		assert.False(t, handled)
	})
}

// Test_driveActivity_escalationSuppressedByLiveClaim: a churn-aborted drive
// with a live claim must not plant a reminder on the new owner.
func Test_driveActivity_escalationSuppressedByLiveClaim(t *testing.T) {
	t.Parallel()

	t.Run("live claim suppresses the escalation", func(t *testing.T) {
		t.Parallel()
		h := newDriveHarness(t)
		h.cancelOn1 = true

		key := inflight.Key("wf::3", testInvocation().GetHistoryEvent())
		call, owner := h.fact.inflight.Acquire(key)
		require.True(t, owner)
		t.Cleanup(func() { call.Finish(nil) })

		a := h.fact.GetOrCreate("wf::3").(*activity)
		name := testActivityName
		require.True(t, a.localDrive(testInvocation(), time.Now().Add(-time.Second), &name))
		h.fact.driveWG.Wait()
		h.fact.escWG.Wait()

		assert.Empty(t, h.sched.snapshotCreates(), "a live claim owns delivery; no durable reminder may be planted")
	})

	t.Run("settled claim still escalates", func(t *testing.T) {
		t.Parallel()
		h := newDriveHarness(t)
		h.cancelOn1 = true

		key := inflight.Key("wf::3", testInvocation().GetHistoryEvent())
		call, owner := h.fact.inflight.Acquire(key)
		require.True(t, owner)
		call.Finish(errStaleClaimEvicted)

		a := h.fact.GetOrCreate("wf::3").(*activity)
		name := testActivityName
		require.True(t, a.localDrive(testInvocation(), time.Now().Add(-time.Second), &name))
		assert.Eventually(t, func() bool {
			return len(h.sched.snapshotCreates()) == 1
		}, time.Second*5, time.Millisecond*10, "without a live claim the durable-reminder escalation must be restored")
		h.fact.driveWG.Wait()
		h.fact.escWG.Wait()
	})
}
