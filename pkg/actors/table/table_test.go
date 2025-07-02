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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/fake"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/events/queue"
)

func Test_HaltAll(t *testing.T) {
	queue := queue.NewProcessor[string, targets.Idlable](queue.Options[string, targets.Idlable]{})

	tble := table.New(table.Options{
		IdlerQueue:      queue,
		ReentrancyStore: reentrancystore.New(),
	})

	deactivations := slice.String()
	tble.RegisterActorTypes(table.RegisterActorTypeOptions{
		Factories: []table.ActorTypeFactory{
			{
				Type: "test1",
				Factory: fake.New("test1", func(f *fake.Fake) {
					f.WithDeactivate(func(context.Context) error {
						deactivations.Append(f.Key())
						return nil
					})
				}),
			},
			{
				Type: "test2",
				Factory: fake.New("test2", func(f *fake.Fake) {
					f.WithDeactivate(func(context.Context) error {
						deactivations.Append(f.Key())
						return nil
					})
				}),
			},
			{
				Type: "test3",
				Factory: fake.New("test3", func(f *fake.Fake) {
					f.WithDeactivate(func(context.Context) error {
						deactivations.Append(f.Key())
						return nil
					})
				}),
			},
		},
	})

	_, _, err := tble.GetOrCreate("test1", "1")
	require.NoError(t, err)
	_, _, err = tble.GetOrCreate("test2", "1")
	require.NoError(t, err)
	_, _, err = tble.GetOrCreate("test2", "2")
	require.NoError(t, err)
	_, _, err = tble.GetOrCreate("test3", "312")
	require.NoError(t, err)
	_, _, err = tble.GetOrCreate("test3", "xyz")
	require.NoError(t, err)

	assert.Equal(t, map[string]int{
		"test1": 1,
		"test2": 2,
		"test3": 2,
	}, tble.Len())

	assert.True(t, tble.IsActorTypeHosted("test1"))
	assert.True(t, tble.IsActorTypeHosted("test2"))
	assert.True(t, tble.IsActorTypeHosted("test3"))
	assert.False(t, tble.IsActorTypeHosted("test4"))

	require.NoError(t, tble.Drain(func(target targets.Interface) bool {
		return target.Key() == "test1||1"
	}))

	assert.ElementsMatch(t, []string{
		"test1||1",
	}, deactivations.Slice())

	require.NoError(t, tble.Drain(func(target targets.Interface) bool {
		return target.Key() == "test2||2"
	}))

	assert.ElementsMatch(t, []string{
		"test1||1",
		"test2||2",
	}, deactivations.Slice())

	require.NoError(t, tble.HaltAll())

	assert.ElementsMatch(t, []string{
		"test1||1", "test2||1", "test2||2", "test3||312", "test3||xyz",
	}, deactivations.Slice())
}

func Test_GetOrCreate_NotRegistered(t *testing.T) {
	queue := queue.NewProcessor[string, targets.Idlable](queue.Options[string, targets.Idlable]{})

	tble := table.New(table.Options{
		IdlerQueue:      queue,
		ReentrancyStore: reentrancystore.New(),
	})

	_, _, err := tble.GetOrCreate("test1", "1")
	require.Error(t, err)
	assert.ErrorIs(t, err, actorerrors.ErrCreatingActor)
}
