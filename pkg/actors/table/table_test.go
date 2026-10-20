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

	recv := func(t *testing.T, ch <-chan []string) []string {
		t.Helper()
		select {
		case got := <-ch:
			return got
		case <-time.After(5 * time.Second):
			require.Fail(t, "no reminder types update")
			return nil
		}
	}
	none := func(t *testing.T, ch <-chan []string) {
		t.Helper()
		select {
		case got := <-ch:
			require.Failf(t, "unexpected reminder types update", "%v", got)
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
		assert.ElementsMatch(t, []string{"plain", "quiet1", "quiet2"}, recv(t, ch))
		assert.ElementsMatch(t, []string{"plain", "quiet1", "quiet2"}, tble.ReminderTypes())

		// No change, no broadcast.
		tble.SetReminderDelivery(true, "quiet1")
		none(t, ch)

		tble.SetReminderDelivery(false, "plain", "quiet1")
		assert.Equal(t, []string{"quiet2"}, recv(t, ch))
		assert.ElementsMatch(t, []string{"plain", "quiet1", "quiet2"}, tble.Types())

		// The placement report is untouched: hosting did not change.
		none(t, typesCh)
	})

	t.Run("an unregistered type is ignored", func(t *testing.T) {
		tble := newTable(t)
		ch := tble.SubscribeToReminderTypeUpdates(t.Context())
		tble.SetReminderDelivery(false, "unknown")
		none(t, ch)
		assert.Equal(t, []string{"plain"}, tble.ReminderTypes())
	})

	t.Run("registering again with reminder delivery enables it", func(t *testing.T) {
		tble := newTable(t)
		tble.RegisterActorTypes(table.RegisterActorTypeOptions{
			Factories: []table.ActorTypeFactory{{Type: "quiet1", Factory: fake.NewFactory()}},
		})
		assert.ElementsMatch(t, []string{"plain", "quiet1"}, tble.ReminderTypes())
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
