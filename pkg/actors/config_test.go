/*
Copyright 2021 The Dapr Authors
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
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/config"

	"github.com/stretchr/testify/assert"
)

const (
	HostAddress = "host"
	AppID       = "testApp"
	Port        = 5000
	Namespace   = "default"
)

func TestConfig(t *testing.T) {
	config := config.ApplicationConfig{
		Entities:                   []string{"1"},
		ActorScanInterval:          "1s",
		ActorIdleTimeout:           "2s",
		DrainOngoingCallTimeout:    "3s",
		DrainRebalancedActors:      true,
		Reentrancy:                 config.ReentrancyConfig{},
		RemindersStoragePartitions: 0,
	}
	c := NewConfig(ConfigOpts{
		HostAddress:   "localhost:5050",
		AppID:         "app1",
		ActorsService: "placement:placement:5050",
		Port:          3500,
		Namespace:     "default",
		AppConfig:     config,
		PodName:       TestPodName,
	})
	assert.Equal(t, "localhost:5050", c.HostAddress)
	assert.Equal(t, "app1", c.AppID)
	assert.Equal(t, "placement:placement:5050", c.ActorsService)
	assert.Equal(t, internal.NewHostedActors([]string{"1"}), c.HostedActorTypes)
	assert.Equal(t, 3500, c.Port)
	assert.Equal(t, "1s", c.ActorDeactivationScanInterval.String())
	assert.Equal(t, "2s", c.ActorIdleTimeout.String())
	assert.Equal(t, "3s", c.DrainOngoingCallTimeout.String())
	assert.True(t, c.DrainRebalancedActors)
	assert.Equal(t, "default", c.Namespace)
	assert.Equal(t, TestPodName, c.PodName)
}

func TestReentrancyConfig(t *testing.T) {
	t.Run("Test empty reentrancy values", func(t *testing.T) {
		config := DefaultAppConfig
		c := NewConfig(ConfigOpts{
			HostAddress:   "localhost:5050",
			AppID:         "app1",
			ActorsService: "placement:placement:5050",
			Port:          3500,
			Namespace:     "default",
			AppConfig:     config,
		})
		assert.False(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 32, *c.Reentrancy.MaxStackDepth)
	})

	t.Run("Test per type reentrancy", func(t *testing.T) {
		appConfig := DefaultAppConfig
		appConfig.EntityConfigs = []config.EntityConfig{
			{
				Entities: []string{"reentrantActor"},
				Reentrancy: config.ReentrancyConfig{
					Enabled: true,
				},
			},
		}
		c := NewConfig(ConfigOpts{
			HostAddress:   "localhost:5050",
			AppID:         "app1",
			ActorsService: "placement:placement:5050",
			Port:          3500,
			Namespace:     "default",
			AppConfig:     appConfig,
		})
		assert.False(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 32, *c.Reentrancy.MaxStackDepth)
		assert.True(t, c.EntityConfigs["reentrantActor"].ReentrancyConfig.Enabled)
	})

	t.Run("Test minimum reentrancy values", func(t *testing.T) {
		appConfig := DefaultAppConfig
		appConfig.Reentrancy = config.ReentrancyConfig{Enabled: true}
		c := NewConfig(ConfigOpts{
			HostAddress:   "localhost:5050",
			AppID:         "app1",
			ActorsService: "placement:placement:5050",
			Port:          3500,
			Namespace:     "default",
			AppConfig:     appConfig,
		})
		assert.True(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 32, *c.Reentrancy.MaxStackDepth)
	})

	t.Run("Test full reentrancy values", func(t *testing.T) {
		appConfig := DefaultAppConfig
		reentrancyLimit := 64
		appConfig.Reentrancy = config.ReentrancyConfig{Enabled: true, MaxStackDepth: &reentrancyLimit}
		c := NewConfig(ConfigOpts{
			HostAddress:   "localhost:5050",
			AppID:         "app1",
			ActorsService: "placement:placement:5050",
			Port:          3500,
			Namespace:     "default",
			AppConfig:     appConfig,
		})
		assert.True(t, c.Reentrancy.Enabled)
		assert.NotNil(t, c.Reentrancy.MaxStackDepth)
		assert.Equal(t, 64, *c.Reentrancy.MaxStackDepth)
	})
}

func TestDefaultConfigValuesSet(t *testing.T) {
	appConfig := config.ApplicationConfig{Entities: []string{"actor1"}}
	config := NewConfig(ConfigOpts{
		HostAddress:   HostAddress,
		AppID:         AppID,
		ActorsService: "placement:placement:4011",
		Port:          Port,
		Namespace:     Namespace,
		AppConfig:     appConfig,
	})

	assert.Equal(t, HostAddress, config.HostAddress)
	assert.Equal(t, AppID, config.AppID)
	assert.Equal(t, "placement:placement:4011", config.ActorsService)
	assert.Equal(t, Port, config.Port)
	assert.Equal(t, Namespace, config.Namespace)
	assert.NotNil(t, config.ActorIdleTimeout)
	assert.NotNil(t, config.ActorDeactivationScanInterval)
	assert.NotNil(t, config.DrainOngoingCallTimeout)
	assert.NotNil(t, config.DrainRebalancedActors)
}

func TestPerActorTypeConfigurationValues(t *testing.T) {
	appConfig := config.ApplicationConfig{
		Entities:                   []string{"actor1", "actor2", "actor3", "actor4"},
		ActorIdleTimeout:           "1s",
		ActorScanInterval:          "2s",
		DrainOngoingCallTimeout:    "5s",
		DrainRebalancedActors:      true,
		RemindersStoragePartitions: 1,
		EntityConfigs: []config.EntityConfig{
			{
				Entities:                []string{"actor1", "actor2"},
				ActorIdleTimeout:        "60s",
				DrainOngoingCallTimeout: "300s",
				DrainRebalancedActors:   false,
			},
			{
				Entities:                []string{"actor3"},
				ActorIdleTimeout:        "5s",
				DrainOngoingCallTimeout: "1s",
				DrainRebalancedActors:   true,
				Reentrancy: config.ReentrancyConfig{
					Enabled: true,
				},
				RemindersStoragePartitions: 10,
			},
		},
	}
	config := NewConfig(ConfigOpts{
		HostAddress:   HostAddress,
		AppID:         AppID,
		ActorsService: "placement:placement:4011",
		Port:          Port,
		Namespace:     Namespace,
		AppConfig:     appConfig,
	})

	// Check the base level items.
	assert.Equal(t, HostAddress, config.HostAddress)
	assert.Equal(t, AppID, config.AppID)
	assert.Contains(t, "placement:placement:4011", config.ActorsService)
	assert.Equal(t, Port, config.Port)
	assert.Equal(t, Namespace, config.Namespace)
	assert.Equal(t, time.Second, config.ActorIdleTimeout)
	assert.Equal(t, time.Second*2, config.ActorDeactivationScanInterval)
	assert.Equal(t, time.Second*5, config.DrainOngoingCallTimeout)
	assert.True(t, config.DrainRebalancedActors)

	// Check the specific actors.
	assert.Equal(t, time.Second*60, config.GetIdleTimeoutForType("actor1"))
	assert.Equal(t, time.Second*300, config.GetDrainOngoingTimeoutForType("actor1"))
	assert.False(t, config.GetDrainRebalancedActorsForType("actor1"))
	assert.False(t, config.GetReentrancyForType("actor1").Enabled)
	assert.Equal(t, 0, config.GetRemindersPartitionCountForType("actor1"))
	assert.Equal(t, time.Second*60, config.GetIdleTimeoutForType("actor2"))
	assert.Equal(t, time.Second*300, config.GetDrainOngoingTimeoutForType("actor2"))
	assert.False(t, config.GetDrainRebalancedActorsForType("actor2"))
	assert.False(t, config.GetReentrancyForType("actor2").Enabled)
	assert.Equal(t, 0, config.GetRemindersPartitionCountForType("actor2"))

	assert.Equal(t, time.Second*5, config.GetIdleTimeoutForType("actor3"))
	assert.Equal(t, time.Second, config.GetDrainOngoingTimeoutForType("actor3"))
	assert.True(t, config.GetDrainRebalancedActorsForType("actor3"))
	assert.True(t, config.GetReentrancyForType("actor3").Enabled)
	assert.Equal(t, 10, config.GetRemindersPartitionCountForType("actor3"))

	assert.Equal(t, time.Second, config.GetIdleTimeoutForType("actor4"))
	assert.Equal(t, time.Second*5, config.GetDrainOngoingTimeoutForType("actor4"))
	assert.True(t, config.GetDrainRebalancedActorsForType("actor4"))
	assert.False(t, config.GetReentrancyForType("actor4").Enabled)
	assert.Equal(t, 1, config.GetRemindersPartitionCountForType("actor4"))
}

func TestOnlyHostedActorTypesAreIncluded(t *testing.T) {
	appConfig := config.ApplicationConfig{
		Entities:                   []string{"actor1", "actor2"},
		ActorIdleTimeout:           "1s",
		ActorScanInterval:          "2s",
		DrainOngoingCallTimeout:    "5s",
		DrainRebalancedActors:      true,
		RemindersStoragePartitions: 1,
		EntityConfigs: []config.EntityConfig{
			{
				Entities:                []string{"actor1", "actor2"},
				ActorIdleTimeout:        "60s",
				DrainOngoingCallTimeout: "300s",
				DrainRebalancedActors:   false,
			},
			{
				Entities:                []string{"actor3"},
				ActorIdleTimeout:        "5s",
				DrainOngoingCallTimeout: "1s",
				DrainRebalancedActors:   true,
				Reentrancy: config.ReentrancyConfig{
					Enabled: true,
				},
				RemindersStoragePartitions: 10,
			},
		},
	}

	config := NewConfig(ConfigOpts{
		HostAddress:   HostAddress,
		AppID:         AppID,
		ActorsService: "placement:placement:4012",
		Port:          Port,
		Namespace:     Namespace,
		AppConfig:     appConfig,
	})

	assert.Contains(t, config.EntityConfigs, "actor1")
	assert.Contains(t, config.EntityConfigs, "actor2")
	assert.NotContains(t, config.EntityConfigs, "actor3")
}
