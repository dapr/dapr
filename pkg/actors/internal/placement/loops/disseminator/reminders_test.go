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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/remotehosts"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func TestReloadReminderTypes(t *testing.T) {
	newDiss := func(t *testing.T, reminderTypes *[]string) (*disseminator, func() []string) {
		t.Helper()
		diss, _, sched := newTestDisseminator(t)
		diss.roundChangedTypes = make(map[string]struct{})
		diss.actorTable = tablefake.New().
			WithTypes(func() []string { return []string{"a", "b"} }).
			WithReminderTypes(func() []string { return *reminderTypes })
		return diss, sched.LastTypes
	}

	round := func(t *testing.T, diss *disseminator, version uint64) {
		t.Helper()
		for _, op := range []loops.OrderOp{loops.OrderLock, loops.OrderUpdate, loops.OrderUnlock} {
			require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
				Order: &loops.Order{Op: op, Version: version, V1Tables: &v1pb.PlacementTables{}},
			}))
		}
	}

	t.Run("a round loads the reminder types, not every hosted type", func(t *testing.T) {
		reminderTypes := []string{"a"}
		diss, last := newDiss(t, &reminderTypes)
		round(t, diss, 1)
		assert.Equal(t, []string{"a"}, last())
	})

	t.Run("a reload before the first round waits for it", func(t *testing.T) {
		reminderTypes := []string{"a"}
		diss, last := newDiss(t, &reminderTypes)
		require.NoError(t, diss.Handle(t.Context(), new(loops.ReloadReminderTypes)))
		assert.Nil(t, last())
	})

	t.Run("a reload after a round loads the reminder types at once", func(t *testing.T) {
		reminderTypes := []string{"a"}
		diss, last := newDiss(t, &reminderTypes)
		round(t, diss, 1)

		reminderTypes = []string{"a", "b"}
		require.NoError(t, diss.Handle(t.Context(), new(loops.ReloadReminderTypes)))
		assert.Equal(t, []string{"a", "b"}, last())

		reminderTypes = []string{}
		require.NoError(t, diss.Handle(t.Context(), new(loops.ReloadReminderTypes)))
		assert.Equal(t, []string{}, last())
	})
}

func TestRemoteHostsUpdatedOnUnlock(t *testing.T) {
	diss, _, _ := newTestDisseminator(t)
	diss.roundChangedTypes = make(map[string]struct{})
	diss.remoteHosts = remotehosts.New()
	ch := diss.remoteHosts.Subscribe(t.Context(), "remote")

	tables := &v1pb.PlacementTables{
		ReplicationFactor: 100,
		Entries: map[string]*v1pb.PlacementTable{
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

	// Not before the round completes.
	select {
	case v := <-ch:
		require.Failf(t, "update before UNLOCK", "%v", v)
	default:
	}

	require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderUnlock, Version: 1},
	}))
	select {
	case v := <-ch:
		assert.True(t, v)
	case <-time.After(5 * time.Second):
		require.Fail(t, "no update at UNLOCK")
	}
}

func TestRefreshRemoteHosts(t *testing.T) {
	diss, _, _ := newTestDisseminator(t)
	diss.roundChangedTypes = make(map[string]struct{})
	diss.remoteHosts = remotehosts.New()

	recv := func(t *testing.T, ch <-chan bool) bool {
		t.Helper()
		select {
		case v := <-ch:
			return v
		case <-time.After(5 * time.Second):
			require.Fail(t, "no update")
			return false
		}
	}

	// Before any round, a refresh tells nothing: the tables are not there.
	early := diss.remoteHosts.Subscribe(t.Context(), "remote")
	require.NoError(t, diss.Handle(t.Context(), new(loops.RefreshRemoteHosts)))
	select {
	case v := <-early:
		require.Failf(t, "update before any round", "%v", v)
	default:
	}

	for _, op := range []loops.OrderOp{loops.OrderLock, loops.OrderUpdate, loops.OrderUnlock} {
		require.NoError(t, diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: op, Version: 1, V1Tables: &v1pb.PlacementTables{}},
		}))
	}
	assert.False(t, recv(t, early))

	// A subscriber after the round is told by the refresh its subscription
	// enqueues.
	late := diss.remoteHosts.Subscribe(t.Context(), "other")
	require.NoError(t, diss.Handle(t.Context(), new(loops.RefreshRemoteHosts)))
	assert.False(t, recv(t, late))
}
