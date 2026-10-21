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

package wfengine

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clocktesting "k8s.io/utils/clock/testing"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/fake"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	backendactors "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/inprocess"
	securityfake "github.com/dapr/dapr/pkg/security/fake"
)

// fallbackTest is an engine with WorkflowsStagedOperations on, a real actor
// table, a presence channel standing in for the placement tables, and a fake
// clock driving the damping period.
type fallbackTest struct {
	wfe      *engine
	table    table.Interface
	presence chan bool
	clock    *clocktesting.FakeClock
	cancel   context.CancelFunc
	done     chan struct{}

	workflowType string
	executorType string
}

func newFallbackTest(t *testing.T) *fallbackTest {
	t.Helper()

	tbl := table.New(table.Options{})
	t.Cleanup(func() { tbl.Close() })
	fa := actorsfake.New().WithTable(func(context.Context) (table.Interface, error) {
		return tbl, nil
	})

	exec := inprocess.NewExecutor()
	abackend, err := backendactors.New(backendactors.Options{
		AppID:                     "wfe-test",
		Namespace:                 "default",
		Actors:                    fa,
		ComponentStore:            compstore.New(),
		WorkflowsStagedOperations: true,
	})
	require.NoError(t, err)

	ft := &fallbackTest{
		table:        tbl,
		presence:     make(chan bool, 1),
		clock:        clocktesting.NewFakeClock(time.Now()),
		done:         make(chan struct{}),
		workflowType: abackend.WorkflowActorType(),
		executorType: abackend.ExecutorActorType(),
	}

	wfe := &engine{
		appID:         "wfe-test",
		namespace:     "default",
		actors:        fa,
		backend:       abackend,
		inProcessExec: exec,
		compStore:     compstore.New(),
		fallback: newFallback(func(context.Context) (<-chan bool, error) {
			return ft.presence, nil
		}),
	}
	wfe.fallback.clock = ft.clock
	wfe.fallback.damping = 10 * time.Second
	t.Cleanup(wfe.fallback.factoryCancel)
	ft.wfe = wfe

	// Mirror Run.
	ctx, cancel := context.WithCancel(t.Context())
	ft.cancel = cancel
	go func() {
		defer close(ft.done)
		wfe.runFallback(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-ft.done
	})
	require.Eventually(t, func() bool {
		wfe.actorRegLock.Lock()
		defer wfe.actorRegLock.Unlock()
		return wfe.fallback.runCtx != nil
	}, 5*time.Second, time.Millisecond)

	return ft
}

// setRemote tells the engine whether another daprd of the app has an
// executor, and waits for it to apply the rule.
func (ft *fallbackTest) setRemote(t *testing.T, remote bool) {
	t.Helper()
	ft.presence <- remote
	require.Eventually(t, func() bool {
		ft.wfe.actorRegLock.Lock()
		defer ft.wfe.actorRegLock.Unlock()
		return len(ft.presence) == 0 && ft.wfe.fallback.remoteKnown && ft.wfe.fallback.remote == remote
	}, 5*time.Second, time.Millisecond)
	// The rule is applied under the lock taken after remote is set.
	ft.wfe.actorRegLock.Lock()
	//nolint:staticcheck // SA2001: waits out the reconcile in progress
	ft.wfe.actorRegLock.Unlock()
}

// hosted reports whether this daprd advertises the workflow actor type to
// placement. A type given up drains: it is no longer advertised, but keeps
// its actors until placement moves them.
func (ft *fallbackTest) hosted() bool {
	return slices.Contains(ft.table.Types(), ft.workflowType)
}

func (ft *fallbackTest) reminders() bool {
	return slices.Contains(ft.table.ReminderTypes(), ft.workflowType)
}

func (ft *fallbackTest) executor() bool {
	return slices.Contains(ft.table.Types(), ft.executorType)
}

func (ft *fallbackTest) actor(t *testing.T) targets.Interface {
	t.Helper()
	a, err := ft.table.GetOrCreate(t.Context(), ft.workflowType, "wf1")
	require.NoError(t, err)
	return a
}

// fallbackHost asserts this daprd is a fallback host.
func (ft *fallbackTest) fallbackHost(t *testing.T) {
	t.Helper()
	assert.True(t, ft.hosted(), "the workflow actor type is hosted")
	assert.False(t, ft.reminders(), "a fallback host takes no reminders")
	assert.False(t, ft.executor(), "a fallback host has no executor actor type")
	assert.False(t, ft.wfe.backend.HasLocalExecutor())
}

// executorHost asserts this daprd hosts the types with a local executor.
func (ft *fallbackTest) executorHost(t *testing.T) {
	t.Helper()
	assert.True(t, ft.hosted(), "the workflow actor type is hosted")
	assert.True(t, ft.reminders(), "the types are advertised for reminders")
	assert.True(t, ft.executor(), "the executor actor type tells the other daprds")
	assert.True(t, ft.wfe.backend.HasLocalExecutor())
}

func (ft *fallbackTest) notHosted(t *testing.T) {
	t.Helper()
	assert.False(t, ft.hosted(), "the workflow actor type is not hosted")
	assert.False(t, ft.executor(), "the executor actor type is not hosted")
}

func TestEngine_FallbackRule(t *testing.T) {
	t.Run("nothing is registered until the placement tables show the executors", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.notHosted(t)
	})

	t.Run("with no executor anywhere this daprd is a fallback host", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.fallbackHost(t)
	})

	t.Run("with an executor on another daprd this daprd hosts nothing", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, true)
		ft.notHosted(t)
	})

	t.Run("a worker connecting hosts the types with the executor actor type", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, true)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.executorHost(t)
	})

	t.Run("the registration is made with the engine's factory context, not the stream's", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, true)

		ctx, cancel := context.WithCancel(t.Context())
		require.NoError(t, ft.wfe.onWorkItemConnection(ctx))
		cancel()
		require.NoError(t, ft.wfe.fallback.factoryCtx.Err())
	})
}

func TestEngine_FallbackTransitions(t *testing.T) {
	t.Run("the first worker switches a fallback host in place", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.fallbackHost(t)
		before := ft.actor(t)

		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.executorHost(t)
		assert.Same(t, before, ft.actor(t), "the workflow actors were never unregistered")

		// A second worker changes nothing.
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		require.NoError(t, ft.wfe.onWorkItemDisconnection(t.Context()))
		ft.executorHost(t)
	})

	t.Run("the last worker leaving with no other executor keeps the types as a fallback host", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		before := ft.actor(t)

		require.NoError(t, ft.wfe.onWorkItemDisconnection(t.Context()))
		ft.fallbackHost(t)
		assert.Same(t, before, ft.actor(t), "the workflow actor type was never left without a host")
	})

	t.Run("the last worker leaving with an executor elsewhere gives the types up at once", func(t *testing.T) {
		ft := newFallbackTest(t)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.setRemote(t, true)
		ft.executorHost(t)

		require.NoError(t, ft.wfe.onWorkItemDisconnection(t.Context()))
		ft.notHosted(t)
	})

	t.Run("a worker reconnecting during a drain is marked available only after the drained actors are halted", func(t *testing.T) {
		ft := newFallbackTest(t)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.setRemote(t, true)
		require.NoError(t, ft.wfe.onWorkItemDisconnection(t.Context()))
		ft.notHosted(t)

		// Stand in for the draining workflow factory, to see what a turn on
		// its actors would see while the reconnect halts them.
		var halted, availableDuringHalt bool
		ft.table.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: ft.workflowType, Factory: fake.NewFactory().WithHaltAll(func(context.Context) error {
				halted = true
				availableDuringHalt = ft.wfe.backend.HasLocalExecutor()
				return nil
			})}},
		})
		ft.table.DrainActorTypes(ft.workflowType)

		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		require.True(t, halted)
		assert.False(t, availableDuringHalt, "a turn on a draining actor must fail at once, not wait on the connecting worker")
		ft.executorHost(t)
	})

	t.Run("an executor appearing elsewhere registers a daprd's fallback once it is gone", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, true)
		ft.notHosted(t)

		ft.setRemote(t, false)
		ft.fallbackHost(t)
	})

	t.Run("an in-process executor counts as a local executor", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		server := mcpServerSpec("srv", startMCPTestServer(t))
		require.NoError(t, ft.wfe.RegisterMCPServer(t.Context(), server, compstore.New(), securityfake.New()))
		ft.executorHost(t)

		ft.wfe.UnregisterMCPServer(server.Name)
		ft.fallbackHost(t)
	})
}

func TestEngine_FallbackDamping(t *testing.T) {
	// step advances the fake clock once the damping timer is armed.
	step := func(t *testing.T, ft *fallbackTest, d time.Duration) {
		t.Helper()
		ft.clock.Step(d)
	}

	t.Run("a fallback host gives the types up only after the damping period", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.setRemote(t, true)
		require.True(t, ft.clock.HasWaiters())

		step(t, ft, 9*time.Second)
		ft.fallbackHost(t)

		step(t, ft, time.Second)
		assert.Eventually(t, func() bool { return !ft.hosted() }, 5*time.Second, time.Millisecond)
		ft.notHosted(t)

		// The types drain: their actors serve the calls placement routes
		// here until a round moves them.
		assert.True(t, ft.table.IsActorTypeHosted(ft.workflowType))
		ft.table.CompleteDrains(func(string) bool { return false })
		assert.False(t, ft.table.IsActorTypeHosted(ft.workflowType))
	})

	t.Run("an executor which leaves within the damping period changes nothing", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)

		// A worker crash looping on another daprd.
		for range 5 {
			ft.setRemote(t, true)
			step(t, ft, 9*time.Second)
			ft.setRemote(t, false)
			assert.False(t, ft.clock.HasWaiters(), "the damping timer is disarmed")
			step(t, ft, 9*time.Second)
			ft.fallbackHost(t)
		}

		// The period restarts each time the executor comes back: it must
		// have been there continuously for the whole of it.
		ft.setRemote(t, true)
		step(t, ft, 9*time.Second)
		ft.fallbackHost(t)
		step(t, ft, time.Second)
		assert.Eventually(t, func() bool { return !ft.hosted() }, 5*time.Second, time.Millisecond)
	})

	t.Run("a local worker connecting during the damping period keeps the types", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.setRemote(t, true)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		assert.False(t, ft.clock.HasWaiters(), "the damping timer is disarmed")

		step(t, ft, time.Minute)
		ft.executorHost(t)
	})
}

func TestEngine_FallbackShutdown(t *testing.T) {
	t.Run("a fallback host gives the types up at shutdown and never registers again", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.fallbackHost(t)

		ft.cancel()
		<-ft.done
		ft.wfe.stopFallback()
		ft.notHosted(t)

		// No fallback registration once Run's context is done.
		ft.wfe.actorRegLock.Lock()
		require.NoError(t, ft.wfe.reconcileFallback(t.Context()))
		ft.wfe.actorRegLock.Unlock()
		ft.notHosted(t)
	})

	t.Run("the last worker leaving after Run's context is done unregisters", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))

		ft.cancel()
		<-ft.done
		ft.wfe.stopFallback()
		ft.executorHost(t)

		require.NoError(t, ft.wfe.onWorkItemDisconnection(t.Context()))
		ft.notHosted(t)
	})
}

func TestEngine_FallbackDisabled(t *testing.T) {
	tbl := table.New(table.Options{})
	t.Cleanup(func() { tbl.Close() })
	fa := actorsfake.New().WithTable(func(context.Context) (table.Interface, error) {
		return tbl, nil
	})
	wfe, _ := newTestEngine(t, fa)
	require.Nil(t, wfe.fallback)

	require.NoError(t, wfe.onWorkItemConnection(t.Context()))
	assert.True(t, tbl.IsActorTypeHosted(wfe.backend.WorkflowActorType()))
	assert.Contains(t, tbl.ReminderTypes(), wfe.backend.WorkflowActorType())
	assert.False(t, tbl.IsActorTypeHosted(wfe.backend.ExecutorActorType()), "no executor actor type without clustered deployment")
	assert.True(t, wfe.backend.HasLocalExecutor())

	require.NoError(t, wfe.onWorkItemDisconnection(t.Context()))
	assert.Empty(t, tbl.Types())
	assert.True(t, wfe.backend.HasLocalExecutor(), "the turn fail-fast is off with the feature off")
}
