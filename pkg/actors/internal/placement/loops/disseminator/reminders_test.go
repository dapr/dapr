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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
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
