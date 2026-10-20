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

package actors

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func newFallbackTestBackend(t *testing.T, fallbackHosts, clustered bool) (*Actors, table.Interface) {
	t.Helper()
	tbl := table.New(table.Options{})
	t.Cleanup(func() { tbl.Close() })
	abe, err := New(Options{
		AppID:          "myapp",
		Namespace:      "default",
		ComponentStore: compstore.New(),
		Actors: actorsfake.New().WithTable(func(context.Context) (table.Interface, error) {
			return tbl, nil
		}),
		WorkflowsFallbackHosts:    fallbackHosts,
		EnableClusteredDeployment: clustered,
	})
	require.NoError(t, err)
	return abe, tbl
}

func TestFallbackSchedulerFailsFast(t *testing.T) {
	wi := &backend.WorkflowWorkItem{InstanceID: "wf1"}
	ai := &backend.ActivityWorkItem{InstanceID: "wf1", NewEvent: &protos.HistoryEvent{}}

	t.Run("with no local executor a turn fails at once", func(t *testing.T) {
		abe, _ := newFallbackTestBackend(t, true, false)
		opts := abe.factoryOptions()
		require.ErrorIs(t, opts.Orchestrator.Scheduler(t.Context(), wi), errNoLocalExecutor)
		require.ErrorIs(t, opts.Activity.Scheduler(t.Context(), ai), errNoLocalExecutor)
	})

	t.Run("with a local executor a turn is queued", func(t *testing.T) {
		abe, _ := newFallbackTestBackend(t, true, false)
		abe.SetExecutorAvailable(true)
		opts := abe.factoryOptions()
		require.NoError(t, opts.Orchestrator.Scheduler(t.Context(), wi))
		require.NoError(t, opts.Activity.Scheduler(t.Context(), ai))
	})

	t.Run("with the feature off a turn waits for the engine as before", func(t *testing.T) {
		abe, _ := newFallbackTestBackend(t, false, false)
		abe.SetExecutorAvailable(false)
		opts := abe.factoryOptions()
		// The channel holds one work item; the second waits for the engine.
		require.NoError(t, opts.Orchestrator.Scheduler(t.Context(), wi))
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		require.ErrorIs(t, opts.Orchestrator.Scheduler(ctx, wi), context.DeadlineExceeded)
	})
}

func TestFallbackRegistration(t *testing.T) {
	for _, clustered := range []bool{false, true} {
		name := "standalone"
		if clustered {
			name = "clustered"
		}
		t.Run(name, func(t *testing.T) {
			abe, tbl := newFallbackTestBackend(t, true, clustered)
			types := []string{abe.workflowActorType, abe.activityActorType, abe.retentionerActorType}
			reminders := func() bool {
				for _, at := range types {
					if !slices.Contains(tbl.ReminderTypes(), at) {
						return false
					}
				}
				return true
			}

			require.NoError(t, abe.RegisterHostedActors(t.Context(), t.Context(), false))
			for _, at := range types {
				assert.True(t, tbl.IsActorTypeHosted(at), at)
				assert.NotContains(t, tbl.ReminderTypes(), at, at)
			}
			assert.False(t, tbl.IsActorTypeHosted(abe.executorActorType))

			require.NoError(t, abe.SetLocalExecutor(t.Context(), t.Context(), true))
			assert.True(t, reminders())
			assert.True(t, tbl.IsActorTypeHosted(abe.executorActorType))

			require.NoError(t, abe.SetLocalExecutor(t.Context(), t.Context(), false))
			for _, at := range types {
				assert.True(t, tbl.IsActorTypeHosted(at), at)
				assert.NotContains(t, tbl.ReminderTypes(), at, at)
			}
			assert.False(t, tbl.IsActorTypeHosted(abe.executorActorType))

			require.NoError(t, abe.UnRegisterActors(t.Context()))
			assert.Empty(t, tbl.Types())

			require.NoError(t, abe.RegisterHostedActors(t.Context(), t.Context(), true))
			assert.True(t, reminders())
			assert.True(t, tbl.IsActorTypeHosted(abe.executorActorType))
			require.NoError(t, abe.UnRegisterActors(t.Context()))
			assert.Empty(t, tbl.Types())
		})
	}
}
