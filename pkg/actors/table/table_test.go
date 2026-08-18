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

	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets/fake"
)

func Test_GetOrCreate_NotRegistered(t *testing.T) {
	tble := table.New(table.Options{
		ReentrancyStore: reentrancystore.New(),
	})

	_, err := tble.GetOrCreate("test1", "1")
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

		_, err := tble.GetOrCreate("test1", "1")
		require.Error(t, err)
		require.ErrorIs(t, err, actorerrors.ErrCreatingActor)
		require.ErrorContains(t, err, "actor hosting is suspended")

		// Factories are retained across suspension.
		tble.ResumeHosting()
		assert.Equal(t, []string{"test1"}, tble.Types())
		assert.True(t, tble.IsActorTypeHosted("test1"))
		_, err = tble.GetOrCreate("test1", "1")
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
