/*
Copyright 2024 The Dapr Authors
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
	"reflect"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/actors/placement"
	"github.com/dapr/dapr/pkg/actors/reminders"
)

func equalFunctions(t *testing.T, func1 any, func2 any) {
	t.Helper()

	assert.Equal(t,
		runtime.FuncForPC(reflect.ValueOf(func1).Pointer()).Name(),
		runtime.FuncForPC(reflect.ValueOf(func2).Pointer()).Name(),
	)
}

func TestConfig_GetPlacementProvider(t *testing.T) {
	t.Run("Empty ActorsService", func(t *testing.T) {
		c := Config{}
		factory, err := c.GetPlacementProvider()
		require.NoError(t, err)
		require.NotNil(t, factory)
		equalFunctions(t, placement.NewActorPlacement, factory)
	})

	t.Run("ActorsService with placement provider", func(t *testing.T) {
		c := Config{
			Config: internal.Config{
				ActorsService: "placement:localhost",
			},
		}
		factory, err := c.GetPlacementProvider()
		require.NoError(t, err)
		require.NotNil(t, factory)
		equalFunctions(t, placement.NewActorPlacement, factory)
	})

	t.Run("ActorsService with invalid provider", func(t *testing.T) {
		c := Config{
			Config: internal.Config{
				ActorsService: "invalidprovider:localhost",
			},
		}
		_, err := c.GetPlacementProvider()
		require.Error(t, err)
		require.EqualError(t, err, "unsupported actor service provider 'invalidprovider'")
	})

	t.Run("ActorsService without provider name", func(t *testing.T) {
		c := Config{
			Config: internal.Config{
				ActorsService: "localhost",
			},
		}
		_, err := c.GetPlacementProvider()
		require.Error(t, err)
		require.EqualError(t, err, "invalid value for the actors service configuration: does not contain the name of the service")
	})
}

func TestConfig_GetRemindersProvider(t *testing.T) {
	t.Run("Empty RemindersService", func(t *testing.T) {
		c := Config{}
		factory, err := c.GetRemindersProvider(nil)
		require.NoError(t, err)
		require.NotNil(t, factory)
		equalFunctions(t, reminders.NewRemindersProvider, factory)
	})

	t.Run("RemindersService with default provider", func(t *testing.T) {
		c := Config{
			Config: internal.Config{
				RemindersService: "default",
			},
		}
		factory, err := c.GetRemindersProvider(nil)
		require.NoError(t, err)
		require.NotNil(t, factory)
	})

	t.Run("RemindersService with custom provider", func(t *testing.T) {
		nilProvider := func(opts internal.ActorsProviderOptions) internal.RemindersProvider {
			return struct{ internal.RemindersProvider }{}
		}
		remindersProviders["custom"] = func(config Config, placement internal.PlacementService) (remindersProviderFactory, error) {
			return nilProvider, nil
		}
		t.Cleanup(func() {
			delete(remindersProviders, "custom")
		})

		c := Config{
			Config: internal.Config{
				RemindersService: "custom:localhost",
			},
		}
		factory, err := c.GetRemindersProvider(nil)
		require.NoError(t, err)
		equalFunctions(t, nilProvider, factory)
	})

	t.Run("RemindersService with invalid provider", func(t *testing.T) {
		c := Config{
			Config: internal.Config{
				RemindersService: "invalidprovider:localhost",
			},
		}
		_, err := c.GetRemindersProvider(nil)
		require.Error(t, err)
		require.EqualError(t, err, "unsupported reminder service provider 'invalidprovider'")
	})

	t.Run("RemindersService without provider name", func(t *testing.T) {
		c := Config{
			Config: internal.Config{
				RemindersService: "localhost",
			},
		}
		_, err := c.GetRemindersProvider(nil)
		require.Error(t, err)
		require.EqualError(t, err, "invalid value for the reminders service configuration: does not contain the name of the service")
	})
}
