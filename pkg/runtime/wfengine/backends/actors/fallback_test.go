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
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets/fake"
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
		WorkflowsStagedOperations: fallbackHosts,
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

func TestFallbackHaltErrorTurningIntoFallbackHost(t *testing.T) {
	abe, tbl := newFallbackTestBackend(t, true, false)
	require.NoError(t, abe.RegisterHostedActors(t.Context(), t.Context(), true))

	// Stand in for the executor factory, to fail the halt of its actors.
	tbl.RegisterActorTypes(table.RegisterActorTypeOptions{
		Factories: []table.ActorTypeFactory{{Type: abe.executorActorType, Factory: fake.NewFactory().WithHaltAll(func(context.Context) error {
			return errors.New("halt failed")
		})}},
	})

	// The executor actor type has left the table by the time the halt
	// fails: the switch is made, so it is not reported as a failed one.
	require.NoError(t, abe.SetLocalExecutor(t.Context(), t.Context(), false))
	assert.False(t, tbl.IsActorTypeHosted(abe.executorActorType))
	for _, at := range []string{abe.workflowActorType, abe.activityActorType, abe.retentionerActorType} {
		assert.True(t, tbl.IsActorTypeHosted(at), at)
		assert.NotContains(t, tbl.ReminderTypes(), at, at)
	}
}

// liveContexts is a factory context which tells which of the contexts made
// from it with context.WithCancel are not cancelled yet: WithCancel registers
// with the AfterFunc of a parent which has one, and stops that registration
// once the child is cancelled.
type liveContexts struct {
	context.Context

	done chan struct{}
	lock sync.Mutex
	err  error
	next int
	live map[int]func()
}

func newLiveContexts(t *testing.T) *liveContexts {
	c := &liveContexts{
		Context: context.Background(),
		done:    make(chan struct{}),
		live:    make(map[int]func()),
	}
	t.Cleanup(c.cancel)
	return c
}

func (c *liveContexts) Done() <-chan struct{} { return c.done }

func (c *liveContexts) Err() error {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.err
}

func (c *liveContexts) AfterFunc(f func()) func() bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	id := c.next
	c.next++
	c.live[id] = f
	return func() bool {
		c.lock.Lock()
		defer c.lock.Unlock()
		_, ok := c.live[id]
		delete(c.live, id)
		return ok
	}
}

// ids returns the contexts made so far which are not cancelled.
func (c *liveContexts) ids() []int {
	c.lock.Lock()
	defer c.lock.Unlock()
	ids := make([]int, 0, len(c.live))
	for id := range c.live {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// cancel cancels c, and with it the contexts made from it.
func (c *liveContexts) cancel() {
	c.lock.Lock()
	c.err = context.Canceled
	close(c.done)
	live := c.live
	c.live = make(map[int]func())
	c.lock.Unlock()
	for _, f := range live {
		f()
	}
}

func TestFallbackFactoryContexts(t *testing.T) {
	for _, clustered := range []bool{false, true} {
		name := "standalone"
		if clustered {
			name = "clustered"
		}
		t.Run(name, func(t *testing.T) {
			abe, _ := newFallbackTestBackend(t, true, clustered)
			factoryCtx := newLiveContexts(t)

			// The workflow factories, then the executor factory.
			require.NoError(t, abe.RegisterHostedActors(t.Context(), factoryCtx, true))
			assert.Equal(t, []int{0, 1}, factoryCtx.ids())

			// Turning into a fallback host ends the executor factory's work.
			require.NoError(t, abe.SetLocalExecutor(t.Context(), factoryCtx, false))
			assert.Equal(t, []int{0}, factoryCtx.ids())

			require.NoError(t, abe.SetLocalExecutor(t.Context(), factoryCtx, true))
			assert.Equal(t, []int{0, 2}, factoryCtx.ids())

			// A later registration ends the work of the factories it
			// replaces; the drained ones keep theirs until then.
			require.NoError(t, abe.DrainActors(t.Context()))
			assert.Equal(t, []int{0, 2}, factoryCtx.ids())
			require.NoError(t, abe.RegisterHostedActors(t.Context(), factoryCtx, true))
			assert.Equal(t, []int{3, 4}, factoryCtx.ids())
		})
	}
}

func TestFallbackDrainedFactoryContexts(t *testing.T) {
	for _, clustered := range []bool{false, true} {
		name := "standalone"
		if clustered {
			name = "clustered"
		}
		t.Run(name, func(t *testing.T) {
			abe, tbl := newFallbackTestBackend(t, true, clustered)
			factoryCtx := newLiveContexts(t)

			require.NoError(t, abe.RegisterHostedActors(t.Context(), factoryCtx, true))
			assert.Equal(t, []int{0, 1}, factoryCtx.ids())

			// The drained factories keep their work while they serve.
			require.NoError(t, abe.DrainActors(t.Context()))
			assert.Equal(t, []int{0, 1}, factoryCtx.ids())

			// The workflow, activity and retention factories share their
			// work: it ends once the table has retired them all.
			tbl.CompleteDrains(t.Context(), func(actorType string) bool {
				return actorType == abe.workflowActorType
			})
			assert.Equal(t, []int{0}, factoryCtx.ids())
			tbl.CompleteDrains(t.Context(), func(string) bool { return false })
			assert.Empty(t, factoryCtx.ids())

			// Unregistering them ends their work too.
			require.NoError(t, abe.RegisterHostedActors(t.Context(), factoryCtx, false))
			assert.Equal(t, []int{2}, factoryCtx.ids())
			require.NoError(t, abe.UnRegisterActors(t.Context()))
			assert.Empty(t, factoryCtx.ids())
		})
	}
}
