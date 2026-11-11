/*
Copyright 2025 The Dapr Authors
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

package table_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	internaltimers "github.com/dapr/dapr/pkg/actors/internal/timers"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/fake"
)

type stubTimerStorage struct {
	sweeps   []func(actorType, actorID string) bool
	onDelete func()
}

func (s *stubTimerStorage) Close() error                                { return nil }
func (s *stubTimerStorage) Create(context.Context, *api.Reminder) error { return nil }
func (s *stubTimerStorage) Delete(context.Context, string)              {}

func (s *stubTimerStorage) List(context.Context, string, string) []*api.Reminder { return nil }
func (s *stubTimerStorage) Get(context.Context, string) *api.Reminder            { return nil }

func (s *stubTimerStorage) DeleteFunc(_ context.Context, fn func(actorType, actorID string) bool) {
	s.sweeps = append(s.sweeps, fn)
	if s.onDelete != nil {
		s.onDelete()
	}
}

func Test_HaltDeletesTimers(t *testing.T) {
	newTable := func(t *testing.T, storage internaltimers.Storage, factory *fake.FakeFactory) table.Interface {
		t.Helper()
		tble := table.New(table.Options{
			ReentrancyStore: reentrancystore.New(),
			Timers:          func() internaltimers.Storage { return storage },
		})
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "test1", Factory: factory}},
		})
		return tble
	}

	t.Run("HaltAll sweeps every timer before halting actors", func(t *testing.T) {
		storage := &stubTimerStorage{}
		var order []string
		storage.onDelete = func() { order = append(order, "sweep") }
		factory := fake.NewFactory().WithHaltAll(func(context.Context) error {
			order = append(order, "halt")
			return nil
		})

		tble := newTable(t, storage, factory)
		require.NoError(t, tble.HaltAll(t.Context()))

		require.Len(t, storage.sweeps, 1)
		assert.True(t, storage.sweeps[0]("any-type", "any-id"))
		assert.Equal(t, []string{"sweep", "halt"}, order)
	})

	t.Run("HaltNonHosted sweeps only timers of non-hosted actors", func(t *testing.T) {
		storage := &stubTimerStorage{}
		tble := newTable(t, storage, fake.NewFactory())

		require.NoError(t, tble.HaltNonHosted(t.Context(), func(req *api.LookupActorRequest) bool {
			return req.ActorID == "hosted"
		}))

		require.Len(t, storage.sweeps, 1)
		assert.False(t, storage.sweeps[0]("test1", "hosted"))
		assert.True(t, storage.sweeps[0]("test1", "moved"))
	})

	t.Run("UnRegisterActorTypes sweeps only the removed types", func(t *testing.T) {
		storage := &stubTimerStorage{}
		tble := newTable(t, storage, fake.NewFactory())

		require.NoError(t, tble.UnRegisterActorTypes("test1", "notregistered"))

		require.Len(t, storage.sweeps, 1)
		assert.True(t, storage.sweeps[0]("test1", "a"))
		assert.True(t, storage.sweeps[0]("notregistered", "a"))
		assert.False(t, storage.sweeps[0]("other", "a"))
	})

	t.Run("nil timers option does not panic", func(t *testing.T) {
		tble := table.New(table.Options{ReentrancyStore: reentrancystore.New()})
		require.NoError(t, tble.HaltAll(t.Context()))
		require.NoError(t, tble.HaltNonHosted(t.Context(), func(*api.LookupActorRequest) bool { return true }))
	})
}

func Test_GetOrCreate_NotRegistered(t *testing.T) {
	tble := table.New(table.Options{
		ReentrancyStore: reentrancystore.New(),
	})

	_, err := tble.GetOrCreate(t.Context(), "test1", "1")
	require.Error(t, err)
	assert.ErrorIs(t, err, actorerrors.ErrCreatingActor)
}

func Test_SuspendHosting(t *testing.T) {
	newRegistered := func(t *testing.T, halted *atomic.Int64) table.Interface {
		t.Helper()
		tble := table.New(table.Options{
			ReentrancyStore: reentrancystore.New(),
		})
		factory := fake.NewFactory()
		if halted != nil {
			factory = factory.WithHaltAll(func(context.Context) error {
				halted.Add(1)
				return nil
			})
		}
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "test1", Factory: factory}},
		})
		return tble
	}

	t.Run("hides types and blocks activation, resume restores", func(t *testing.T) {
		var halted atomic.Int64
		tble := newRegistered(t, &halted)

		assert.Equal(t, []string{"test1"}, tble.Types())
		assert.True(t, tble.IsActorTypeHosted("test1"))

		require.NoError(t, tble.SuspendHosting(t.Context()))
		assert.Empty(t, tble.Types())
		assert.False(t, tble.IsActorTypeHosted("test1"))
		assert.Equal(t, int64(1), halted.Load())

		_, err := tble.GetOrCreate(t.Context(), "test1", "1")
		require.Error(t, err)
		require.ErrorIs(t, err, actorerrors.ErrCreatingActor)
		require.ErrorContains(t, err, "actor hosting is suspended")

		// Factories are retained across suspension.
		tble.ResumeHosting()
		assert.Equal(t, []string{"test1"}, tble.Types())
		assert.True(t, tble.IsActorTypeHosted("test1"))
		_, err = tble.GetOrCreate(t.Context(), "test1", "1")
		require.NoError(t, err)
	})

	t.Run("suspend and resume are idempotent", func(t *testing.T) {
		var halted atomic.Int64
		tble := newRegistered(t, &halted)

		require.NoError(t, tble.SuspendHosting(t.Context()))
		require.NoError(t, tble.SuspendHosting(t.Context()))
		assert.Equal(t, int64(1), halted.Load())

		tble.ResumeHosting()
		tble.ResumeHosting()
		assert.Equal(t, []string{"test1"}, tble.Types())
	})

	t.Run("broadcasts empty then full type list", func(t *testing.T) {
		tble := newRegistered(t, nil)

		ch, types := tble.SubscribeToTypeUpdates(t.Context())
		assert.Equal(t, []string{"test1"}, types)

		require.NoError(t, tble.SuspendHosting(t.Context()))
		select {
		case got := <-ch:
			assert.Empty(t, got)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timed out waiting for suspend broadcast")
		}

		tble.ResumeHosting()
		select {
		case got := <-ch:
			assert.Equal(t, []string{"test1"}, got)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timed out waiting for resume broadcast")
		}
	})

	t.Run("registration while suspended advertises nothing until resume", func(t *testing.T) {
		tble := table.New(table.Options{
			ReentrancyStore: reentrancystore.New(),
			StartSuspended:  true,
		})

		ch, types := tble.SubscribeToTypeUpdates(t.Context())
		assert.Empty(t, types)

		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "test1", Factory: fake.NewFactory()}},
		})
		select {
		case got := <-ch:
			assert.Empty(t, got)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timed out waiting for register broadcast")
		}
		assert.Empty(t, tble.Types())
		assert.False(t, tble.IsActorTypeHosted("test1"))

		tble.ResumeHosting()
		select {
		case got := <-ch:
			assert.Equal(t, []string{"test1"}, got)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timed out waiting for resume broadcast")
		}
		assert.Equal(t, []string{"test1"}, tble.Types())
	})
}

func Test_ReminderDelivery(t *testing.T) {
	newTable := func(t *testing.T) table.Interface {
		t.Helper()
		tble := table.New(table.Options{ReentrancyStore: reentrancystore.New()})
		t.Cleanup(func() { require.NoError(t, tble.Close()) })
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "plain", Factory: fake.NewFactory()}},
		})
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{
				{Type: "quiet1", Factory: fake.NewFactory()},
				{Type: "quiet2", Factory: fake.NewFactory()},
			},
			NoReminderDelivery: true,
		})
		return tble
	}

	// recv waits for a reminder types update and returns the reminder types.
	recv := func(t *testing.T, tble table.Interface, ch <-chan struct{}) []string {
		t.Helper()
		select {
		case <-ch:
			return tble.ReminderTypes()
		case <-time.After(5 * time.Second):
			require.Fail(t, "no reminder types update")
			return nil
		}
	}
	none := func(t *testing.T, ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
			require.Fail(t, "unexpected reminder types update")
		case <-time.After(100 * time.Millisecond):
		}
	}

	t.Run("types registered without reminder delivery are hosted but not reminder types", func(t *testing.T) {
		tble := newTable(t)
		assert.ElementsMatch(t, []string{"plain", "quiet1", "quiet2"}, tble.Types())
		assert.Equal(t, []string{"plain"}, tble.ReminderTypes())
		assert.True(t, tble.IsActorTypeHosted("quiet1"))
	})

	t.Run("toggling reminder delivery keeps the types hosted and broadcasts the change", func(t *testing.T) {
		tble := newTable(t)
		ch := tble.SubscribeToReminderTypeUpdates(t.Context())
		typesCh, _ := tble.SubscribeToTypeUpdates(t.Context())

		tble.SetReminderDelivery(true, "quiet1", "quiet2")
		assert.ElementsMatch(t, []string{"plain", "quiet1", "quiet2"}, recv(t, tble, ch))
		assert.ElementsMatch(t, []string{"plain", "quiet1", "quiet2"}, tble.ReminderTypes())

		// No change, no broadcast.
		tble.SetReminderDelivery(true, "quiet1")
		none(t, ch)

		tble.SetReminderDelivery(false, "plain", "quiet1")
		assert.Equal(t, []string{"quiet2"}, recv(t, tble, ch))
		assert.ElementsMatch(t, []string{"plain", "quiet1", "quiet2"}, tble.Types())

		// The placement report is untouched: hosting did not change.
		select {
		case got := <-typesCh:
			require.Failf(t, "unexpected types update", "%v", got)
		case <-time.After(100 * time.Millisecond):
		}
	})

	t.Run("an unregistered type is ignored", func(t *testing.T) {
		tble := newTable(t)
		ch := tble.SubscribeToReminderTypeUpdates(t.Context())
		tble.SetReminderDelivery(false, "unknown")
		none(t, ch)
		assert.Equal(t, []string{"plain"}, tble.ReminderTypes())
	})

	t.Run("registering again with reminder delivery enables it and broadcasts the change", func(t *testing.T) {
		tble := newTable(t)
		ch := tble.SubscribeToReminderTypeUpdates(t.Context())
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "quiet1", Factory: fake.NewFactory()}},
		})
		assert.ElementsMatch(t, []string{"plain", "quiet1"}, tble.ReminderTypes())
		assert.ElementsMatch(t, []string{"plain", "quiet1"}, recv(t, tble, ch))

		// No change, no broadcast.
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "quiet1", Factory: fake.NewFactory()}},
		})
		none(t, ch)
	})

	t.Run("registering a new type leaves its reminder types to the placement round", func(t *testing.T) {
		tble := newTable(t)
		ch := tble.SubscribeToReminderTypeUpdates(t.Context())
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{
				{Type: "quiet1", Factory: fake.NewFactory()},
				{Type: "new", Factory: fake.NewFactory()},
			},
		})
		assert.ElementsMatch(t, []string{"plain", "quiet1", "new"}, tble.ReminderTypes())
		none(t, ch)
	})

	t.Run("unregistering a type forgets it has no reminder delivery", func(t *testing.T) {
		tble := newTable(t)
		require.NoError(t, tble.UnRegisterActorTypes("quiet1"))
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "quiet1", Factory: fake.NewFactory()}},
		})
		assert.ElementsMatch(t, []string{"plain", "quiet1"}, tble.ReminderTypes())
	})

	t.Run("suspended hosting advertises no reminder types", func(t *testing.T) {
		tble := newTable(t)
		require.NoError(t, tble.SuspendHosting(t.Context()))
		assert.Empty(t, tble.ReminderTypes())
	})
}

func Test_DrainActorTypes(t *testing.T) {
	newTable := func(t *testing.T) (table.Interface, *atomic.Int32) {
		t.Helper()
		var halted atomic.Int32
		tble := table.New(table.Options{ReentrancyStore: reentrancystore.New()})
		t.Cleanup(func() { require.NoError(t, tble.Close()) })
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{
				{Type: "kept", Factory: fake.NewFactory()},
				{Type: "drained", Factory: fake.NewFactory().WithHaltAll(func(context.Context) error {
					halted.Add(1)
					return nil
				})},
			},
		})
		return tble, &halted
	}

	t.Run("a draining type leaves the report but keeps serving", func(t *testing.T) {
		tble, halted := newTable(t)
		typesCh, _ := tble.SubscribeToTypeUpdates(t.Context())

		tble.DrainActorTypes("drained", "unknown")
		select {
		case got := <-typesCh:
			assert.Equal(t, []string{"kept"}, got)
		case <-time.After(5 * time.Second):
			require.Fail(t, "no types update")
		}
		assert.Equal(t, []string{"kept"}, tble.Types())
		assert.Equal(t, []string{"kept"}, tble.ReminderTypes())
		assert.True(t, tble.IsActorTypeHosted("drained"))
		_, err := tble.GetOrCreate(t.Context(), "drained", "1")
		require.NoError(t, err)

		// A round which still places the type here keeps it.
		tble.CompleteDrains(t.Context(), func(actorType string) bool { return actorType == "drained" })
		assert.True(t, tble.IsActorTypeHosted("drained"))
		assert.Zero(t, halted.Load())

		// The round which moves it away unregisters it.
		tble.CompleteDrains(t.Context(), func(string) bool { return false })
		assert.False(t, tble.IsActorTypeHosted("drained"))
		_, err = tble.GetOrCreate(t.Context(), "drained", "1")
		require.ErrorIs(t, err, actorerrors.ErrCreatingActor)
		assert.Eventually(t, func() bool { return halted.Load() == 1 }, 5*time.Second, time.Millisecond)
		assert.True(t, tble.IsActorTypeHosted("kept"))
	})

	t.Run("registering a draining type again halts its draining factory first", func(t *testing.T) {
		tble, halted := newTable(t)
		tble.DrainActorTypes("drained")

		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "drained", Factory: fake.NewFactory()}},
		})
		assert.EqualValues(t, 1, halted.Load())
		assert.ElementsMatch(t, []string{"kept", "drained"}, tble.Types())

		// No longer draining: a round does not unregister it.
		tble.CompleteDrains(t.Context(), func(string) bool { return false })
		assert.True(t, tble.IsActorTypeHosted("drained"))
	})

	t.Run("completing a drain halts the drained factory before returning", func(t *testing.T) {
		tble := table.New(table.Options{ReentrancyStore: reentrancystore.New()})
		t.Cleanup(func() { require.NoError(t, tble.Close()) })

		var halted atomic.Int32
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "drained", Factory: fake.NewFactory().WithHaltAll(func(context.Context) error {
				halted.Add(1)
				return nil
			})}},
		})
		tble.DrainActorTypes("drained")
		tble.CompleteDrains(t.Context(), func(string) bool { return false })
		assert.EqualValues(t, 1, halted.Load())
		assert.False(t, tble.IsActorTypeHosted("drained"))

		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "drained", Factory: fake.NewFactory()}},
		})
		assert.True(t, tble.IsActorTypeHosted("drained"))
		assert.EqualValues(t, 1, halted.Load())
	})

	t.Run("unregistering a draining type ends its drain", func(t *testing.T) {
		tble, halted := newTable(t)
		tble.DrainActorTypes("drained")
		require.NoError(t, tble.UnRegisterActorTypes("drained"))
		assert.EqualValues(t, 1, halted.Load())

		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "drained", Factory: fake.NewFactory()}},
		})
		assert.ElementsMatch(t, []string{"kept", "drained"}, tble.Types())
	})
}

// retirerFactory counts the table's calls to its Retire.
type retirerFactory struct {
	*fake.FakeFactory
	retired atomic.Int32
}

func (f *retirerFactory) Retire() { f.retired.Add(1) }

func Test_RetireFactory(t *testing.T) {
	newTable := func(t *testing.T) (table.Interface, *retirerFactory) {
		t.Helper()
		tble := table.New(table.Options{ReentrancyStore: reentrancystore.New()})
		t.Cleanup(func() { require.NoError(t, tble.Close()) })
		f := &retirerFactory{FakeFactory: fake.NewFactory()}
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "retired", Factory: f}},
		})
		return tble, f
	}

	t.Run("halting the actors of a kept factory does not retire it", func(t *testing.T) {
		tble, f := newTable(t)
		require.NoError(t, tble.HaltAll(t.Context()))
		require.NoError(t, tble.SuspendHosting(t.Context()))
		tble.ResumeHosting()
		assert.Zero(t, f.retired.Load())
	})

	t.Run("completing a drain retires the drained factory once", func(t *testing.T) {
		tble, f := newTable(t)
		tble.DrainActorTypes("retired")
		assert.Zero(t, f.retired.Load())
		tble.CompleteDrains(t.Context(), func(string) bool { return true })
		assert.Zero(t, f.retired.Load())

		tble.CompleteDrains(t.Context(), func(string) bool { return false })
		assert.EqualValues(t, 1, f.retired.Load())
		tble.CompleteDrains(t.Context(), func(string) bool { return false })
		require.NoError(t, tble.UnRegisterActorTypes("retired"))
		assert.EqualValues(t, 1, f.retired.Load())
	})

	t.Run("unregistering a type retires its factory", func(t *testing.T) {
		tble, f := newTable(t)
		require.NoError(t, tble.UnRegisterActorTypes("retired"))
		assert.EqualValues(t, 1, f.retired.Load())
	})

	t.Run("registering a draining type again retires its draining factory", func(t *testing.T) {
		tble, f := newTable(t)
		tble.DrainActorTypes("retired")
		next := &retirerFactory{FakeFactory: fake.NewFactory()}
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "retired", Factory: next}},
		})
		assert.EqualValues(t, 1, f.retired.Load())
		assert.Zero(t, next.retired.Load())
	})

	t.Run("registering a type which is not draining again retires its factory", func(t *testing.T) {
		tble, f := newTable(t)
		next := &retirerFactory{FakeFactory: fake.NewFactory()}
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "retired", Factory: next}},
		})
		assert.EqualValues(t, 1, f.retired.Load())
		assert.Zero(t, next.retired.Load())
	})
}

func Test_DrainedFactoryHalt(t *testing.T) {
	// haltingFactory holds the actor ID "held" until its halt, which blocks
	// until release is closed. The actors it creates have type name.
	type haltingFactory struct {
		*fake.FakeFactory
		started chan struct{}
		release chan struct{}
	}
	newHaltingFactory := func(name string, halt func(context.Context)) *haltingFactory {
		var halted atomic.Bool
		f := &haltingFactory{started: make(chan struct{}), release: make(chan struct{})}
		f.FakeFactory = fake.NewFactory().
			WithGetOrCreate(func(string) targets.Interface { return fake.New(name) }).
			WithExists(func(actorID string) bool { return actorID == "held" && !halted.Load() }).
			WithHaltAll(func(ctx context.Context) error {
				close(f.started)
				if halt != nil {
					halt(ctx)
				}
				<-f.release
				halted.Store(true)
				return nil
			})
		return f
	}
	newFactory := func() targets.Factory {
		return fake.NewFactory().WithGetOrCreate(func(string) targets.Interface { return fake.New("new") })
	}
	created := func(t *testing.T, tble table.Interface, actorID string) string {
		t.Helper()
		target, err := tble.GetOrCreate(t.Context(), "drained", actorID)
		require.NoError(t, err)
		return target.Type()
	}
	wait := func(t *testing.T, ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			require.Fail(t, "timed out")
		}
	}
	pending := func(t *testing.T, ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
			require.Fail(t, "registered before the draining factory halted")
		default:
		}
	}
	newTable := func(t *testing.T, old targets.Factory) table.Interface {
		t.Helper()
		tble := table.New(table.Options{ReentrancyStore: reentrancystore.New()})
		t.Cleanup(func() { require.NoError(t, tble.Close()) })
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "drained", Factory: old}},
		})
		tble.DrainActorTypes("drained")
		return tble
	}
	register := func(tble table.Interface, factory targets.Factory) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			tble.RegisterActorTypes(table.RegisterActorTypeOptions{
				Factories: []table.ActorTypeFactory{{Type: "drained", Factory: factory}},
			})
		}()
		return done
	}

	t.Run("a type registered again while its draining factory halts stays registered", func(t *testing.T) {
		old := newHaltingFactory("old", nil)
		tble := newTable(t, old)

		registered := register(tble, newFactory())
		wait(t, old.started)

		// The draining factory keeps serving the actor ID it holds; the
		// others are created in the new factory.
		assert.True(t, tble.IsActorTypeHosted("drained"))
		assert.Contains(t, tble.Types(), "drained")
		assert.Equal(t, "old", created(t, tble, "held"))
		assert.True(t, tble.ActorExists("drained", "held"))
		assert.Equal(t, "new", created(t, tble, "other"))
		pending(t, registered)

		close(old.release)
		wait(t, registered)
		assert.Equal(t, "new", created(t, tble, "held"))
	})

	t.Run("registering again waits for the halt completing the drain", func(t *testing.T) {
		old := newHaltingFactory("old", nil)
		tble := newTable(t, old)

		completed := make(chan struct{})
		go func() {
			defer close(completed)
			tble.CompleteDrains(t.Context(), func(string) bool { return false })
		}()
		wait(t, old.started)
		assert.False(t, tble.IsActorTypeHosted("drained"))

		registered := register(tble, newFactory())
		require.Eventually(t, func() bool { return tble.IsActorTypeHosted("drained") }, 5*time.Second, time.Millisecond)
		assert.Equal(t, "old", created(t, tble, "held"))
		assert.Equal(t, "new", created(t, tble, "other"))
		pending(t, registered)

		close(old.release)
		wait(t, completed)
		wait(t, registered)
		assert.Equal(t, "new", created(t, tble, "held"))
	})

	t.Run("a halt which leaves an actor re-created during it halts again", func(t *testing.T) {
		// A call routed to the draining factory re-creates the actor its
		// halt just deactivated: the halt makes no progress, and must go on
		// until the factory is empty rather than retire it with the actor.
		var (
			halts atomic.Int32
			left  atomic.Int32
		)
		left.Store(1)
		retired := &retirerFactory{
			FakeFactory: fake.NewFactory().
				WithLen(func() int { return int(left.Load()) }).
				WithHaltAll(func(context.Context) error {
					if halts.Add(1) > 1 {
						left.Store(0)
					}
					return nil
				}),
		}
		tble := newTable(t, retired)

		wait(t, register(tble, newFactory()))
		assert.EqualValues(t, 2, halts.Load())
		assert.EqualValues(t, 1, retired.retired.Load())
	})

	t.Run("a halt waiting on a turn which creates an actor of the same type completes", func(t *testing.T) {
		// An orchestrator turn starting a child workflow creates an actor of
		// its own type, and the halt of its factory waits for that turn.
		var tble table.Interface
		turn := make(chan error, 1)
		old := newHaltingFactory("old", func(ctx context.Context) {
			_, err := tble.GetOrCreate(ctx, "drained", "child")
			turn <- err
		})
		tble = newTable(t, old)
		close(old.release)

		wait(t, register(tble, newFactory()))
		require.NoError(t, <-turn)
	})
}
