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
		assert.Equal(t, table.HostedTypes{Active: []string{"test1"}}, types)

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
			assert.Equal(t, table.HostedTypes{Active: []string{"test1"}}, got)
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
			assert.Equal(t, table.HostedTypes{Active: []string{"test1"}}, got)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timed out waiting for resume broadcast")
		}
		assert.Equal(t, []string{"test1"}, tble.Types())
	})
}

func Test_StandbyTypes(t *testing.T) {
	newTable := func() table.Interface {
		return table.New(table.Options{ReentrancyStore: reentrancystore.New()})
	}

	recv := func(t *testing.T, ch <-chan table.HostedTypes) table.HostedTypes {
		t.Helper()
		select {
		case got := <-ch:
			return got
		case <-time.After(time.Second * 5):
			require.Fail(t, "timed out waiting for type broadcast")
			return table.HostedTypes{}
		}
	}

	t.Run("standby types are hosted but advertised apart", func(t *testing.T) {
		tble := newTable()
		ch, types := tble.SubscribeToTypeUpdates(t.Context())
		assert.Empty(t, types)

		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "active", Factory: fake.NewFactory()}},
		})
		assert.Equal(t, table.HostedTypes{Active: []string{"active"}}, recv(t, ch))

		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "standby", Factory: fake.NewFactory()}},
			Standby:   true,
		})
		assert.Equal(t, table.HostedTypes{Active: []string{"active"}, Standby: []string{"standby"}}, recv(t, ch))

		assert.Equal(t, []string{"active"}, tble.Types())
		assert.Equal(t, []string{"standby"}, tble.StandbyTypes())
		assert.True(t, tble.IsActorTypeHosted("standby"))
		_, err := tble.GetOrCreate(t.Context(), "standby", "id")
		require.NoError(t, err)
	})

	t.Run("unregistering a standby type removes it", func(t *testing.T) {
		tble := newTable()
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "wf", Factory: fake.NewFactory()}},
			Standby:   true,
		})
		ch, types := tble.SubscribeToTypeUpdates(t.Context())
		assert.Equal(t, table.HostedTypes{Standby: []string{"wf"}}, types)

		require.NoError(t, tble.UnRegisterActorTypes("wf"))
		assert.Empty(t, recv(t, ch))
		assert.False(t, tble.IsActorTypeHosted("wf"))

		// Registering the type again as active does not keep it standby.
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "wf", Factory: fake.NewFactory()}},
		})
		assert.Equal(t, table.HostedTypes{Active: []string{"wf"}}, recv(t, ch))
		assert.Empty(t, tble.StandbyTypes())
	})

	t.Run("suspended hosting advertises no standby types", func(t *testing.T) {
		tble := newTable()
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "wf", Factory: fake.NewFactory()}},
			Standby:   true,
		})
		require.NoError(t, tble.SuspendHosting(t.Context()))
		assert.Empty(t, tble.StandbyTypes())
		tble.ResumeHosting()
		assert.Equal(t, []string{"wf"}, tble.StandbyTypes())
	})
}

func Test_ReplaceActorTypes(t *testing.T) {
	newTable := func() table.Interface {
		return table.New(table.Options{ReentrancyStore: reentrancystore.New()})
	}

	t.Run("flips the types to standby in one broadcast, halting the replaced factories", func(t *testing.T) {
		tble := newTable()
		var activeHalted, executorHalted atomic.Bool
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{
				{Type: "wf", Factory: fake.NewFactory().WithHaltAll(func(context.Context) error {
					activeHalted.Store(true)
					return nil
				})},
				{Type: "executor", Factory: fake.NewFactory().WithHaltAll(func(context.Context) error {
					executorHalted.Store(true)
					return nil
				})},
			},
		})

		ch, _ := tble.SubscribeToTypeUpdates(t.Context())
		var updates []table.HostedTypes
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case got := <-ch:
					// Every report hosts the workflow actor type.
					assert.True(t, tble.IsActorTypeHosted("wf"))
					updates = append(updates, got)
				case <-time.After(time.Millisecond * 200):
					return
				}
			}
		}()

		standby := fake.New("wf")
		require.NoError(t, tble.ReplaceActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "wf", Factory: fake.NewFactory().WithGetOrCreate(
				func(string) targets.Interface { return standby },
			)}},
			Standby: true,
		}, "executor"))
		<-done

		assert.Equal(t, []table.HostedTypes{{Standby: []string{"wf"}}}, updates)
		assert.True(t, activeHalted.Load())
		assert.True(t, executorHalted.Load())
		assert.False(t, tble.IsActorTypeHosted("executor"))

		got, err := tble.GetOrCreate(t.Context(), "wf", "id")
		require.NoError(t, err)
		assert.Same(t, standby, got)
	})

	t.Run("a create of an actor the replaced factory holds waits for its halt", func(t *testing.T) {
		tble := newTable()
		var held atomic.Bool
		held.Store(true)
		release := make(chan struct{})
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "wf", Factory: fake.NewFactory().
				WithExists(func(id string) bool { return id == "busy" && held.Load() }).
				WithHaltAll(func(context.Context) error {
					// A turn in flight on "busy" holds the halt.
					<-release
					held.Store(false)
					return nil
				}),
			}},
		})

		replaced := make(chan error, 1)
		go func() {
			replaced <- tble.ReplaceActorTypes(table.RegisterActorTypeOptions{
				Factories: []table.ActorTypeFactory{{Type: "wf", Factory: fake.NewFactory()}},
				Standby:   true,
			})
		}()
		require.Eventually(t, func() bool {
			return len(tble.StandbyTypes()) == 1
		}, time.Second*5, time.Millisecond*10)

		// An actor the replaced factory does not hold is created at once.
		_, err := tble.GetOrCreate(t.Context(), "wf", "idle")
		require.NoError(t, err)

		created := make(chan error, 1)
		go func() {
			_, err := tble.GetOrCreate(t.Context(), "wf", "busy")
			created <- err
		}()
		select {
		case err := <-created:
			require.Fail(t, "created the actor while the replaced factory held it", "err: %v", err)
		case <-time.After(time.Millisecond * 200):
		}

		// A waiting create honors its context.
		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond*50)
		defer cancel()
		_, err = tble.GetOrCreate(ctx, "wf", "busy")
		require.ErrorIs(t, err, context.DeadlineExceeded)

		close(release)
		require.NoError(t, <-replaced)
		select {
		case err := <-created:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			require.Fail(t, "create still waiting after the halt")
		}
	})
}
