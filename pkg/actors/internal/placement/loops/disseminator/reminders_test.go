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

package disseminator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

// roundV1 runs a complete v1 dissemination round with tables.
func roundV1(t *testing.T, diss *disseminator, version uint64, tables *v1pb.PlacementTables) {
	t.Helper()
	for _, op := range []loops.OrderOp{loops.OrderLock, loops.OrderUpdate, loops.OrderUnlock} {
		require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: op, Version: version, V1Tables: tables},
		}))
	}
}

// roundV2 runs a complete v2 dissemination round over scope with tables.
func roundV2(t *testing.T, diss *disseminator, seq uint64, scope []string, tables map[string][]string) {
	t.Helper()
	versions := make(map[string]uint64, len(tables))
	for actorType := range tables {
		versions[actorType] = seq
	}
	for _, order := range []*loops.Order{
		{Op: loops.OrderLock, Version: seq, Scope: scope, Partial: true},
		{Op: loops.OrderUpdate, Version: seq, Partial: true, Versions: versions, V2Tables: v2Tables(tables)},
		{Op: loops.OrderUnlock, Version: seq, Scope: scope, Partial: true},
	} {
		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{Order: order}))
	}
}

// collectRounds records the RoundCompleted events diss sends the placement
// loop.
func collectRounds(diss *disseminator) *[]*loops.RoundCompleted {
	rounds := new([]*loops.RoundCompleted)
	diss.placementLoop = loopfake.New[loops.EventPlace]().
		WithEnqueue(func(e loops.EventPlace) {
			if r, ok := e.(*loops.RoundCompleted); ok {
				*rounds = append(*rounds, r)
			}
		})
	return rounds
}

func TestReloadReminderTypes(t *testing.T) {
	newDiss := func(t *testing.T, v2 bool, reminderTypes *[]string) (*disseminator, func() []string) {
		t.Helper()
		diss, _, sched := newTestDisseminator(t)
		diss.schedulerPlacement = v2
		diss.actorTable = tablefake.New().
			WithTypes(func() []string { return []string{"a", "b"} }).
			WithReminderTypes(func() []string { return *reminderTypes })
		return diss, sched.LastTypes
	}

	round := func(t *testing.T, diss *disseminator, version uint64) {
		t.Helper()
		if diss.schedulerPlacement {
			roundV2(t, diss, version, nil, map[string][]string{"a": {"localhost:3500"}})
		} else {
			roundV1(t, diss, version, &v1pb.PlacementTables{})
		}
	}

	for name, v2 := range map[string]bool{"v1": false, "v2": true} {
		t.Run(name, func(t *testing.T) {
			t.Run("a round loads the reminder types, not every hosted type", func(t *testing.T) {
				reminderTypes := []string{"a"}
				diss, last := newDiss(t, v2, &reminderTypes)
				round(t, diss, 1)
				assert.Equal(t, []string{"a"}, last())
			})

			t.Run("a reload before the first round waits for it", func(t *testing.T) {
				reminderTypes := []string{"a"}
				diss, last := newDiss(t, v2, &reminderTypes)
				require.NoError(t, diss.Handle(t.Context(), new(loops.ReloadReminderTypes)))
				assert.Nil(t, last())
			})

			t.Run("a reload after a round loads the reminder types at once", func(t *testing.T) {
				reminderTypes := []string{"a"}
				diss, last := newDiss(t, v2, &reminderTypes)
				round(t, diss, 1)

				reminderTypes = []string{"a", "b"}
				require.NoError(t, diss.Handle(t.Context(), new(loops.ReloadReminderTypes)))
				assert.Equal(t, []string{"a", "b"}, last())

				reminderTypes = []string{}
				require.NoError(t, diss.Handle(t.Context(), new(loops.ReloadReminderTypes)))
				assert.Equal(t, []string{}, last())
			})

			t.Run("a reload on a new stream waits for its first round", func(t *testing.T) {
				reminderTypes := []string{"a"}
				diss, last := newDiss(t, v2, &reminderTypes)
				round(t, diss, 1)
				assert.Equal(t, []string{"a"}, last())

				// The placement loop builds a new disseminator for each
				// stream session.
				next, nextLast := newDiss(t, v2, &reminderTypes)
				reminderTypes = []string{"a", "b"}
				require.NoError(t, next.Handle(t.Context(), new(loops.ReloadReminderTypes)))
				assert.Nil(t, nextLast())

				round(t, next, 1)
				assert.Equal(t, []string{"a", "b"}, nextLast())
			})
		})
	}
}

func TestRoundCompletedV1(t *testing.T) {
	diss, _, _ := newTestDisseminator(t)
	rounds := collectRounds(diss)

	tables := &v1pb.PlacementTables{
		ReplicationFactor: 100,
		Entries: map[string]*v1pb.PlacementTable{
			"local": {LoadMap: map[string]*v1pb.Host{
				"localhost:3500": {Name: "localhost:3500", Id: "self", Port: 3500},
			}},
			"remote": {LoadMap: map[string]*v1pb.Host{
				"10.0.0.2:3500": {Name: "10.0.0.2:3500", Id: "other", Port: 3500},
			}},
		},
	}
	for _, op := range []loops.OrderOp{loops.OrderLock, loops.OrderUpdate} {
		require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: op, Version: 1, V1Tables: tables},
		}))
	}
	assert.Empty(t, *rounds, "not before the round completes")

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderUnlock, Version: 1},
	}))
	require.Len(t, *rounds, 1)
	assert.Equal(t, map[string]bool{"remote": true}, (*rounds)[0].Remote)
	assert.Empty(t, (*rounds)[0].Pending)
	assert.False(t, (*rounds)[0].PendingAll)
}

func TestRoundCompletedV2(t *testing.T) {
	t.Run("a round reports the types other hosts host", func(t *testing.T) {
		diss := newTestDisseminatorV2(t)
		collectAcks(diss)
		rounds := collectRounds(diss)

		roundV2(t, diss, 1, nil, map[string][]string{
			"local":  {"localhost:3500"},
			"remote": {"10.0.0.2:3500"},
		})
		require.Len(t, *rounds, 1)
		assert.Equal(t, map[string]bool{"remote": true}, (*rounds)[0].Remote)
		assert.Empty(t, (*rounds)[0].Pending)
		assert.False(t, (*rounds)[0].PendingAll)
	})

	t.Run("types of a round still in progress are pending", func(t *testing.T) {
		diss := newTestDisseminatorV2(t)
		collectAcks(diss)
		rounds := collectRounds(diss)

		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderLock, Version: 7, Scope: []string{"open"}, Partial: true},
		}))
		roundV2(t, diss, 8, []string{"done"}, map[string][]string{"done": {"10.0.0.2:3500"}})

		require.Len(t, *rounds, 1)
		assert.Equal(t, map[string]struct{}{"open": {}}, (*rounds)[0].Pending)
		assert.False(t, (*rounds)[0].PendingAll)
	})

	t.Run("a startup snapshot in progress leaves every type pending", func(t *testing.T) {
		diss := newTestDisseminatorV2(t)
		collectAcks(diss)
		rounds := collectRounds(diss)

		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderLock, Version: 1, Partial: true},
		}))
		roundV2(t, diss, 2, []string{"done"}, map[string][]string{"done": {"10.0.0.2:3500"}})

		require.Len(t, *rounds, 1)
		assert.True(t, (*rounds)[0].PendingAll)
	})
}

func TestPendingTypesV2(t *testing.T) {
	diss, _, _ := newTestDisseminator(t)
	diss.v2Rounds = map[uint64]*v2Round{
		1: {scope: []string{"a"}, changed: map[string]struct{}{"b": {}}},
	}
	all, pending := diss.pendingTypes()
	assert.False(t, all)
	assert.Equal(t, map[string]struct{}{"a": {}, "b": {}}, pending)

	// The startup snapshot has an empty scope and covers every type.
	diss.v2Rounds[2] = &v2Round{changed: map[string]struct{}{}}
	all, _ = diss.pendingTypes()
	assert.True(t, all)
}

func TestCompleteDrainsOnUnlock(t *testing.T) {
	t.Run("v1", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var placed []bool
		diss.actorTable = tablefake.New().WithCompleteDrains(func(_ context.Context, fn func(string) bool) {
			placed = append(placed, fn("local"), fn("remote"))
		})

		tables := &v1pb.PlacementTables{
			ReplicationFactor: 100,
			Entries: map[string]*v1pb.PlacementTable{
				"local": {LoadMap: map[string]*v1pb.Host{
					"localhost:3500": {Name: "localhost:3500", Id: "self", Port: 3500},
				}},
				"remote": {LoadMap: map[string]*v1pb.Host{
					"10.0.0.2:3500": {Name: "10.0.0.2:3500", Id: "other", Port: 3500},
				}},
			},
		}
		for _, op := range []loops.OrderOp{loops.OrderLock, loops.OrderUpdate} {
			require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
				Order: &loops.Order{Op: op, Version: 1, V1Tables: tables},
			}))
		}
		assert.Empty(t, placed, "drains complete only once the round does")

		require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderUnlock, Version: 1},
		}))
		assert.Equal(t, []bool{true, false}, placed)
	})

	t.Run("v2", func(t *testing.T) {
		diss := newTestDisseminatorV2(t)
		collectAcks(diss)

		var placed []bool
		diss.actorTable = tablefake.New().WithCompleteDrains(func(_ context.Context, fn func(string) bool) {
			placed = append(placed, fn("local"), fn("remote"), fn("open"))
		})

		// A round still in progress covers "open", so it keeps draining
		// although no table places it here.
		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderLock, Version: 7, Scope: []string{"open"}, Partial: true},
		}))
		roundV2(t, diss, 8, []string{"local", "remote"}, map[string][]string{
			"local":  {"localhost:3500"},
			"remote": {"10.0.0.2:3500"},
		})
		assert.Equal(t, []bool{true, false, true}, placed)
	})
}
