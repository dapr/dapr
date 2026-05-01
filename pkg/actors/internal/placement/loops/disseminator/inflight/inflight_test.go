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

package inflight

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func newTables(rf int64, entries map[string]map[string]int64) *v1pb.PlacementTables {
	out := &v1pb.PlacementTables{
		ReplicationFactor: rf,
		Entries:           map[string]*v1pb.PlacementTable{},
	}
	for atype, hosts := range entries {
		lm := map[string]*v1pb.Host{}
		for name, port := range hosts {
			lm[name] = &v1pb.Host{
				Name: name,
				Id:   name,
				Port: port,
			}
		}
		out.Entries[atype] = &v1pb.PlacementTable{LoadMap: lm}
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestSet_DiffsChangedTypes(t *testing.T) {
	t.Run("first set returns all types as changed", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1"})
		got := i.Set(newTables(100, map[string]map[string]int64{
			"a": {"h:1": 1},
			"b": {"h:1": 1},
		}), 1)
		assert.ElementsMatch(t, []string{"a", "b"}, got)
	})

	t.Run("identical second set returns no changed types", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1"})
		i.Set(newTables(100, map[string]map[string]int64{
			"a": {"h:1": 1},
			"b": {"h:1": 1},
		}), 1)
		got := i.Set(newTables(100, map[string]map[string]int64{
			"a": {"h:1": 1},
			"b": {"h:1": 1},
		}), 2)
		assert.Empty(t, got)
	})

	t.Run("only changed type is reported", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1"})
		i.Set(newTables(100, map[string]map[string]int64{
			"a": {"h:1": 1},
			"b": {"h:1": 1},
		}), 1)
		got := i.Set(newTables(100, map[string]map[string]int64{
			"a": {"h:1": 1, "h:2": 2}, // host added to a
			"b": {"h:1": 1},
		}), 2)
		assert.Equal(t, []string{"a"}, sortedCopy(got))
	})

	t.Run("removed type is reported", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1"})
		i.Set(newTables(100, map[string]map[string]int64{
			"a": {"h:1": 1},
			"b": {"h:1": 1},
		}), 1)
		got := i.Set(newTables(100, map[string]map[string]int64{
			"a": {"h:1": 1},
			// b removed
		}), 2)
		assert.Equal(t, []string{"b"}, sortedCopy(got))
	})

	t.Run("added type is reported", func(t *testing.T) {
		i := New(Options{Hostname: "h", Port: "1"})
		i.Set(newTables(100, map[string]map[string]int64{
			"a": {"h:1": 1},
		}), 1)
		got := i.Set(newTables(100, map[string]map[string]int64{
			"a": {"h:1": 1},
			"c": {"h:1": 1}, // new type
		}), 2)
		assert.Equal(t, []string{"c"}, sortedCopy(got))
	})
}

func TestLockUnlockTypes_Queueing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	i := New(Options{Hostname: "h", Port: "1"})
	// Install initial table containing both types so resolve does not error.
	i.Set(newTables(100, map[string]map[string]int64{
		"locked":   {"h:1": 1},
		"unlocked": {"h:1": 1},
	}), 1)

	// Open the lock loop so claims can be issued.
	i.Open(ctx)

	// Block one type.
	i.LockTypes([]string{"locked"})

	// A LookupRequest for the unlocked type should resolve immediately.
	respCh := make(chan *loops.LookupResponse, 1)
	i.AcquireLookup(&loops.LookupRequest{
		Request:  &api.LookupActorRequest{ActorType: "unlocked", ActorID: "a"},
		Context:  ctx,
		Response: respCh,
	})
	select {
	case <-respCh:
	case <-time.After(time.Second):
		require.Fail(t, "AcquireLookup for unlocked type should not have queued")
	}

	// A LookupRequest for the locked type should NOT resolve until UnlockTypes.
	blockedCh := make(chan *loops.LookupResponse, 1)
	i.AcquireLookup(&loops.LookupRequest{
		Request:  &api.LookupActorRequest{ActorType: "locked", ActorID: "a"},
		Context:  ctx,
		Response: blockedCh,
	})
	select {
	case <-blockedCh:
		require.Fail(t, "AcquireLookup for locked type should have queued")
	case <-time.After(50 * time.Millisecond):
	}

	// Unblock; queued response should drain.
	i.UnlockTypes([]string{"locked"})
	select {
	case <-blockedCh:
	case <-time.After(time.Second):
		require.Fail(t, "AcquireLookup queued response should drain after UnlockTypes")
	}

	// Subsequent lookups for the now-unblocked type should be immediate.
	immediateCh := make(chan *loops.LookupResponse, 1)
	i.AcquireLookup(&loops.LookupRequest{
		Request:  &api.LookupActorRequest{ActorType: "locked", ActorID: "b"},
		Context:  ctx,
		Response: immediateCh,
	})
	select {
	case <-immediateCh:
	case <-time.After(time.Second):
		require.Fail(t, "AcquireLookup for newly-unblocked type should not have queued")
	}

	i.Close(nil)
}

func TestAcquireBeforeOpen_QueuesUntilOpen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	i := New(Options{Hostname: "h", Port: "1"})
	i.Set(newTables(100, map[string]map[string]int64{
		"a": {"h:1": 1},
	}), 1)

	respCh := make(chan *loops.LookupResponse, 1)
	i.AcquireLookup(&loops.LookupRequest{
		Request:  &api.LookupActorRequest{ActorType: "a", ActorID: "id"},
		Context:  ctx,
		Response: respCh,
	})
	select {
	case <-respCh:
		require.Fail(t, "AcquireLookup before Open should queue")
	case <-time.After(50 * time.Millisecond):
	}

	i.Open(ctx)
	select {
	case <-respCh:
	case <-time.After(time.Second):
		require.Fail(t, "AcquireLookup queued before Open should drain after Open")
	}

	i.Close(nil)
}

func TestAcquire_QueuesWhileTypeBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	i := New(Options{Hostname: "h", Port: "1"})
	i.Set(newTables(100, map[string]map[string]int64{
		"locked":   {"h:1": 1},
		"unlocked": {"h:1": 1},
	}), 1)
	i.Open(ctx)
	i.LockTypes([]string{"locked"})

	immediate := make(chan *loops.LockResponse, 1)
	i.Acquire(&loops.LockRequest{
		ActorType: "unlocked",
		Context:   ctx,
		Response:  immediate,
	})
	select {
	case <-immediate:
	case <-time.After(time.Second):
		require.Fail(t, "Acquire for unlocked type should not have queued")
	}

	queued := make(chan *loops.LockResponse, 1)
	i.Acquire(&loops.LockRequest{
		ActorType: "locked",
		Context:   ctx,
		Response:  queued,
	})
	select {
	case <-queued:
		require.Fail(t, "Acquire for locked type should have queued")
	case <-time.After(50 * time.Millisecond):
	}

	i.UnlockTypes([]string{"locked"})
	select {
	case <-queued:
	case <-time.After(time.Second):
		require.Fail(t, "queued Acquire should drain after UnlockTypes")
	}

	i.Close(nil)
}

func TestCancelClaimsForTypes_DrainsMatchingClaims(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	i := New(Options{Hostname: "h", Port: "1"})
	i.Set(newTables(100, map[string]map[string]int64{
		"a": {"h:1": 1},
		"b": {"h:1": 1},
	}), 1)
	i.Open(ctx)

	// Issue claims for both types and confirm they resolve.
	for _, atype := range []string{"a", "b"} {
		respCh := make(chan *loops.LockResponse, 1)
		i.Acquire(&loops.LockRequest{
			ActorType: atype,
			Context:   ctx,
			Response:  respCh,
		})
		select {
		case resp := <-respCh:
			require.NotNil(t, resp.Cancel)
		case <-time.After(time.Second):
			require.Fail(t, "Acquire should resolve")
		}
	}

	cancelErr := errors.New("placement table updated")
	i.CancelClaimsForTypes([]string{"a"}, cancelErr)

	// CancelClaimsForTypes blocks until drain completes; once it returns
	// the affected claims have been torn down. Acquiring a fresh claim for
	// type "a" should still succeed because the loop is not closed.
	respCh := make(chan *loops.LockResponse, 1)
	i.Acquire(&loops.LockRequest{
		ActorType: "a",
		Context:   ctx,
		Response:  respCh,
	})
	select {
	case <-respCh:
	case <-time.After(time.Second):
		require.Fail(t, "Acquire after CancelClaimsForTypes should still succeed")
	}

	i.Close(nil)
}

func TestCancelClaimsForTypes_NoLockNoop(t *testing.T) {
	i := New(Options{Hostname: "h", Port: "1"})
	// No Open - lock loop is nil. Should not panic and should return.
	i.CancelClaimsForTypes([]string{"a"}, errors.New("noop"))
}

func TestCancelClaimsForTypes_EmptyTypesNoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	i := New(Options{Hostname: "h", Port: "1"})
	i.Open(ctx)
	// Empty set should return without enqueueing.
	i.CancelClaimsForTypes(nil, errors.New("noop"))
	i.Close(nil)
}

func TestIsActorHostedNoLock(t *testing.T) {
	i := New(Options{Hostname: "h", Port: "1"})
	i.Set(newTables(100, map[string]map[string]int64{
		"local": {"h:1": 1},
	}), 1)

	// Type that exists and resolves to the local host.
	assert.True(t, i.IsActorHostedNoLock(&api.LookupActorRequest{
		ActorType: "local", ActorID: "x",
	}))

	// Unknown type returns false (resolve errors out).
	assert.False(t, i.IsActorHostedNoLock(&api.LookupActorRequest{
		ActorType: "unknown", ActorID: "x",
	}))
}

func TestIsActorHostedNoLock_RemoteHost(t *testing.T) {
	i := New(Options{Hostname: "h", Port: "1"})
	// Single host that is NOT us.
	i.Set(newTables(100, map[string]map[string]int64{
		"remote": {"other:9": 9},
	}), 1)

	assert.False(t, i.IsActorHostedNoLock(&api.LookupActorRequest{
		ActorType: "remote", ActorID: "x",
	}))
}

func TestSetDrainOngoingCallTimeout_StoresValues(t *testing.T) {
	i := New(Options{Hostname: "h", Port: "1"})
	drain := true
	timeout := 7 * time.Second

	i.SetDrainOngoingCallTimeout(&drain, &timeout)

	assert.Equal(t, &drain, i.drainRebalancedActors.Load())
	assert.Equal(t, &timeout, i.drainOngoingCallTimeout.Load())
}

func TestClose_NoLoopNoop(t *testing.T) {
	i := New(Options{Hostname: "h", Port: "1"})
	// No Open - lock loop is nil. Close should be a no-op.
	i.Close(nil)
}

func TestOpenIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	i := New(Options{Hostname: "h", Port: "1"})
	i.Set(newTables(100, map[string]map[string]int64{"a": {"h:1": 1}}), 1)

	i.Open(ctx)
	first := i.lock
	i.Open(ctx)
	assert.Same(t, first, i.lock, "Open must not replace an active lock loop")

	i.Close(nil)
}

func TestUnlockTypes_DrainsQueuedFns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	i := New(Options{Hostname: "h", Port: "1"})
	i.Set(newTables(100, map[string]map[string]int64{
		"locked": {"h:1": 1},
	}), 1)
	i.Open(ctx)
	i.LockTypes([]string{"locked"})

	respCh := make(chan *loops.LookupResponse, 1)
	i.AcquireLookup(&loops.LookupRequest{
		Request:  &api.LookupActorRequest{ActorType: "locked", ActorID: "x"},
		Context:  ctx,
		Response: respCh,
	})

	// Without UnlockTypes the queued fn should remain queued.
	require.Contains(t, i.queued, "locked")
	require.Len(t, i.queued["locked"], 1)

	i.UnlockTypes([]string{"locked"})

	select {
	case <-respCh:
	case <-time.After(time.Second):
		require.Fail(t, "queued response should drain after UnlockTypes")
	}
	assert.NotContains(t, i.queued, "locked")
	assert.NotContains(t, i.blockedTypes, "locked")

	i.Close(nil)
}
