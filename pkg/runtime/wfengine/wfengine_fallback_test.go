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
	"errors"
	"slices"
	"sync/atomic"
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
// clock driving the settle and damping periods.
type fallbackTest struct {
	wfe   *engine
	table table.Interface
	// presence is unbuffered: a send returns once runFallback received it.
	presence chan bool
	clock    *clocktesting.FakeClock
	cancel   context.CancelFunc
	done     chan struct{}

	// remote is the last value sent on presence, if any.
	remote      bool
	remoteKnown bool

	// tableDown fails every call for the actor table, as an actor runtime
	// which is not ready does.
	tableDown atomic.Bool

	workflowType string
	executorType string
}

func newFallbackTest(t *testing.T) *fallbackTest {
	t.Helper()

	tbl := table.New(table.Options{})
	t.Cleanup(func() { tbl.Close() })
	ft := &fallbackTest{
		table:    tbl,
		presence: make(chan bool),
		clock:    clocktesting.NewFakeClock(time.Now()),
		done:     make(chan struct{}),
	}
	fa := actorsfake.New().WithTable(func(context.Context) (table.Interface, error) {
		if ft.tableDown.Load() {
			return nil, errors.New("actor table unavailable")
		}
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

	ft.workflowType = abackend.WorkflowActorType()
	ft.executorType = abackend.ExecutorActorType()

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
	wfe.fallback.settle = 3 * time.Second
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

// send sends remote on presence twice: the second send returns once
// runFallback has applied the rule after the first, which it does with the
// state the engine had when the first was sent.
func (ft *fallbackTest) send(t *testing.T, remote bool) {
	t.Helper()
	for range 2 {
		select {
		case ft.presence <- remote:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "runFallback does not receive")
		}
	}
	ft.remote, ft.remoteKnown = remote, true
}

// setRemote tells the engine whether another daprd of the app has an
// executor, and waits for it to apply the rule.
func (ft *fallbackTest) setRemote(t *testing.T, remote bool) {
	t.Helper()
	ft.send(t, remote)
}

// sync waits for runFallback to apply the rule to the engine's state, such as
// after a local executor left.
func (ft *fallbackTest) sync(t *testing.T) {
	t.Helper()
	require.True(t, ft.remoteKnown, "sync sends the last remote value again")
	ft.send(t, ft.remote)
}

// disconnect has a worker leave, and waits for runFallback to apply the rule.
func (ft *fallbackTest) disconnect(t *testing.T) {
	t.Helper()
	require.NoError(t, ft.wfe.onWorkItemDisconnection(t.Context()))
	ft.sync(t)
}

// settling reports whether runFallback waits on the clock: the settle period
// of a daprd which hosts nothing, the damping period of a fallback host, or a
// retry.
func (ft *fallbackTest) settling() bool {
	return ft.clock.HasWaiters()
}

// settle steps the fake clock past the settle period armed by a daprd which
// hosts nothing, and waits for it to host the types as a fallback host.
func (ft *fallbackTest) settle(t *testing.T) {
	t.Helper()
	require.Eventually(t, ft.settling, 5*time.Second, time.Millisecond, "the settle timer is armed")
	ft.notHosted(t)
	ft.clock.Step(ft.wfe.fallback.settle)
	require.Eventually(t, ft.hosted, 5*time.Second, time.Millisecond)
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
		ft.settle(t)
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

func TestEngine_FallbackRetry(t *testing.T) {
	t.Run("a failed registration is applied again after the retry interval", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.tableDown.Store(true)
		ft.setRemote(t, false)
		require.True(t, ft.settling())
		ft.clock.Step(ft.wfe.fallback.settle)
		// The registration fails once the settle period passed, and is tried
		// again after the retry interval.
		require.Eventually(t, ft.clock.HasWaiters, 5*time.Second, time.Millisecond)
		ft.notHosted(t)

		ft.tableDown.Store(false)
		ft.clock.Step(fallbackRetryInterval)
		assert.Eventually(t, ft.hosted, 5*time.Second, time.Millisecond)
		ft.fallbackHost(t)
	})

	t.Run("a watch which closes ends the fallback hosting", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.settle(t)
		ft.fallbackHost(t)

		// As the actor runtime shuts down: the fallback host gives the types
		// up, and the connection callbacks act alone.
		close(ft.presence)
		<-ft.done
		ft.notHosted(t)

		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.executorHost(t)
		require.NoError(t, ft.wfe.onWorkItemDisconnection(t.Context()))
		ft.notHosted(t)
	})
}

func TestEngine_FallbackTransitions(t *testing.T) {
	t.Run("the first worker switches a fallback host in place", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.settle(t)
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

		ft.disconnect(t)
		ft.fallbackHost(t)
		assert.Same(t, before, ft.actor(t), "the workflow actor type was never left without a host")
	})

	t.Run("the last worker leaving keeps the types as a fallback host even if halting the executor actors fails", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.executorHost(t)
		before := ft.actor(t)

		// Stand in for the executor factory, to fail the halt of its actors.
		ft.table.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: ft.executorType, Factory: fake.NewFactory().WithHaltAll(func(context.Context) error {
				return errors.New("halt failed")
			})}},
		})

		// The executor actor type has left the table by the time the halt
		// fails, so the switch is made in place, not by giving the types up
		// and registering them again.
		ft.disconnect(t)
		ft.fallbackHost(t)
		assert.Same(t, before, ft.actor(t), "the workflow actors were never unregistered")
	})

	t.Run("the last worker leaving with an executor elsewhere gives the types up at once", func(t *testing.T) {
		ft := newFallbackTest(t)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.setRemote(t, true)
		ft.executorHost(t)

		ft.disconnect(t)
		ft.notHosted(t)
	})

	t.Run("a worker reconnecting during a drain is marked available only after the drained actors are halted", func(t *testing.T) {
		ft := newFallbackTest(t)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.setRemote(t, true)
		ft.disconnect(t)
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
		ft.settle(t)
		ft.fallbackHost(t)
	})

	t.Run("an in-process executor counts as a local executor", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		server := mcpServerSpec("srv", startMCPTestServer(t))
		require.NoError(t, ft.wfe.RegisterMCPServer(t.Context(), server, compstore.New(), securityfake.New()))
		ft.executorHost(t)

		ft.wfe.UnregisterMCPServer(server.Name)
		ft.sync(t)
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
		ft.settle(t)
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
		ft.table.CompleteDrains(t.Context(), func(string) bool { return false })
		assert.False(t, ft.table.IsActorTypeHosted(ft.workflowType))
	})

	t.Run("an executor which leaves within the damping period changes nothing", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.settle(t)

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
		ft.settle(t)
		ft.setRemote(t, true)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.sync(t)
		assert.False(t, ft.clock.HasWaiters(), "the damping timer is disarmed")

		step(t, ft, time.Minute)
		ft.executorHost(t)
	})

	t.Run("a failed switch to a local executor keeps the damping period running", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.settle(t)
		ft.setRemote(t, true)
		require.Eventually(t, ft.clock.HasWaiters, 5*time.Second, time.Millisecond)

		// An in-process executor whose registration fails rolls its count
		// back, and the period it interrupted goes on.
		ft.tableDown.Store(true)
		require.Error(t, ft.wfe.EnsureActorsRegistered(t.Context()))
		ft.tableDown.Store(false)
		ft.sync(t)
		assert.True(t, ft.clock.HasWaiters(), "the damping timer is still armed")
		ft.fallbackHost(t)

		step(t, ft, 10*time.Second)
		assert.Eventually(t, func() bool { return !ft.hosted() }, 5*time.Second, time.Millisecond)
		ft.notHosted(t)
	})
}

func TestEngine_FallbackSettle(t *testing.T) {
	// neverHosted asserts this daprd does not take the types meanwhile: a
	// settle timer which fired would have them registered by then.
	neverHosted := func(t *testing.T, ft *fallbackTest) {
		t.Helper()
		assert.Never(t, ft.hosted, 200*time.Millisecond, 10*time.Millisecond)
		ft.notHosted(t)
	}

	t.Run("a daprd which hosts nothing becomes a fallback host only after the settle period", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.notHosted(t)
		require.True(t, ft.settling())

		ft.clock.Step(2 * time.Second)
		neverHosted(t, ft)

		ft.clock.Step(time.Second)
		assert.Eventually(t, ft.hosted, 5*time.Second, time.Millisecond)
		ft.fallbackHost(t)
		assert.False(t, ft.settling())
	})

	t.Run("an executor appearing elsewhere within the settle period cancels it", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.clock.Step(2 * time.Second)
		neverHosted(t, ft)

		// The daprd with the worker connected to placement after this one.
		ft.setRemote(t, true)
		assert.False(t, ft.settling(), "the settle timer is disarmed")
		ft.clock.Step(time.Minute)
		neverHosted(t, ft)
	})

	t.Run("the settle period restarts each time no executor is left elsewhere", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.clock.Step(2 * time.Second)
		ft.setRemote(t, true)
		ft.setRemote(t, false)
		require.True(t, ft.settling())

		// Past the first period, short of the second.
		ft.clock.Step(2 * time.Second)
		neverHosted(t, ft)

		ft.clock.Step(time.Second)
		assert.Eventually(t, ft.hosted, 5*time.Second, time.Millisecond)
		ft.fallbackHost(t)
	})

	t.Run("a local worker connecting during the settle period hosts the types with it", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.clock.Step(2 * time.Second)
		neverHosted(t, ft)

		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.executorHost(t)
		ft.sync(t)
		assert.False(t, ft.settling(), "the settle timer is disarmed")
		ft.clock.Step(time.Minute)
		ft.executorHost(t)
	})

	t.Run("a failed local worker connection during the settle period keeps it running", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)

		// An in-process executor whose registration fails rolls its count
		// back, and the period it interrupted goes on.
		ft.tableDown.Store(true)
		require.Error(t, ft.wfe.EnsureActorsRegistered(t.Context()))
		ft.tableDown.Store(false)
		ft.sync(t)
		assert.True(t, ft.settling(), "the settle timer is still armed")
		ft.notHosted(t)

		ft.settle(t)
		ft.fallbackHost(t)
	})

	t.Run("the last local worker leaving with no executor elsewhere switches in place at once", func(t *testing.T) {
		ft := newFallbackTest(t)
		require.NoError(t, ft.wfe.onWorkItemConnection(t.Context()))
		ft.setRemote(t, false)
		ft.executorHost(t)
		assert.False(t, ft.settling(), "a daprd with an executor does not settle")
		before := ft.actor(t)

		ft.disconnect(t)
		ft.fallbackHost(t)
		assert.False(t, ft.settling(), "the daprd hosted the types already")
		assert.Same(t, before, ft.actor(t), "the workflow actor type was never left without a host")
	})

	t.Run("a daprd which gave the types up after the damping period settles again", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.settle(t)
		ft.setRemote(t, true)
		ft.clock.Step(ft.wfe.fallback.damping)
		require.Eventually(t, func() bool { return !ft.hosted() }, 5*time.Second, time.Millisecond)

		ft.setRemote(t, false)
		require.True(t, ft.settling())
		neverHosted(t, ft)
		ft.settle(t)
		ft.fallbackHost(t)
	})

	t.Run("shutdown during the settle period never registers", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		require.True(t, ft.settling())

		ft.cancel()
		<-ft.done
		ft.wfe.stopFallback()
		assert.False(t, ft.settling(), "the settle timer is disarmed")
		ft.clock.Step(time.Minute)
		neverHosted(t, ft)
	})
}

func TestEngine_FallbackShutdown(t *testing.T) {
	t.Run("a fallback host gives the types up at shutdown and never registers again", func(t *testing.T) {
		ft := newFallbackTest(t)
		ft.setRemote(t, false)
		ft.settle(t)
		ft.fallbackHost(t)

		ft.cancel()
		<-ft.done
		ft.wfe.stopFallback()
		ft.notHosted(t)

		// No fallback registration once Run's context is done.
		ft.wfe.actorRegLock.Lock()
		wait, err := ft.wfe.reconcileFallback(t.Context(), true, true)
		ft.wfe.actorRegLock.Unlock()
		require.NoError(t, err)
		assert.False(t, wait)
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
