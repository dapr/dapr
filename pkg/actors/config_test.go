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

	appConfig "github.com/dapr/dapr/pkg/config"

	"github.com/stretchr/testify/assert"
)

const (
	HostAddress      = "host"
	AppID            = "testApp"
	PlacementAddress = "placement"
	Port             = 5000
	Namespace        = "default"
)

func TestDefaultConfigValuesSet(t *testing.T) {
	appConfig := appConfig.ApplicationConfig{Entities: []string{"actor1"}}
	config := NewConfig(ConfigOpts{
		HostAddress:        HostAddress,
		AppID:              AppID,
		PlacementAddresses: []string{PlacementAddress},
		Port:               Port,
		Namespace:          Namespace,
		AppConfig:          appConfig,
	})

	assert.Equal(t, HostAddress, config.coreConfig.HostAddress)
	assert.Equal(t, AppID, config.coreConfig.AppID)
	assert.Contains(t, config.coreConfig.PlacementAddresses, PlacementAddress)
	assert.Equal(t, Port, config.coreConfig.Port)
	assert.Equal(t, Namespace, config.coreConfig.Namespace)
	assert.NotNil(t, config.coreConfig.ActorIdleTimeout)
	assert.NotNil(t, config.coreConfig.ActorDeactivationScanInterval)
	assert.NotNil(t, config.coreConfig.DrainOngoingCallTimeout)
	assert.NotNil(t, config.coreConfig.DrainRebalancedActors)
}

func TestPerActorTypeConfigurationValues(t *testing.T) {
	appConfig := appConfig.ApplicationConfig{
		Entities:                   []string{"actor1", "actor2", "actor3", "actor4"},
		ActorIdleTimeout:           "1s",
		ActorScanInterval:          "2s",
		DrainOngoingCallTimeout:    "5s",
		DrainRebalancedActors:      true,
		RemindersStoragePartitions: 1,
		EntityConfigs: []appConfig.EntityConfig{
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
				Reentrancy: appConfig.ReentrancyConfig{
					Enabled: true,
				},
				RemindersStoragePartitions: 10,
			},
		},
	}
	config := NewConfig(ConfigOpts{
		HostAddress:        HostAddress,
		AppID:              AppID,
		PlacementAddresses: []string{PlacementAddress},
		Port:               Port,
		Namespace:          Namespace,
		AppConfig:          appConfig,
	})

	// Check the base level items.
	assert.Equal(t, HostAddress, config.coreConfig.HostAddress)
	assert.Equal(t, AppID, config.coreConfig.AppID)
	assert.Contains(t, config.coreConfig.PlacementAddresses, PlacementAddress)
	assert.Equal(t, Port, config.coreConfig.Port)
	assert.Equal(t, Namespace, config.coreConfig.Namespace)
	assert.Equal(t, time.Second, config.coreConfig.ActorIdleTimeout)
	assert.Equal(t, time.Second*2, config.coreConfig.ActorDeactivationScanInterval)
	assert.Equal(t, time.Second*5, config.coreConfig.DrainOngoingCallTimeout)
	assert.True(t, config.coreConfig.DrainRebalancedActors)

	// Check the specific actors.
	assert.Equal(t, time.Second*60, config.GetIdleTimeoutForType("actor1"))
	assert.Equal(t, time.Second*300, config.GetDrainOngoingTimeoutForType("actor1"))
	assert.False(t, config.GetDrainRebalancedActorsForType("actor1"))
	assert.False(t, config.coreConfig.GetReentrancyForType("actor1").Enabled)
	assert.Equal(t, 0, config.coreConfig.GetRemindersPartitionCountForType("actor1"))
	assert.Equal(t, time.Second*60, config.GetIdleTimeoutForType("actor2"))
	assert.Equal(t, time.Second*300, config.GetDrainOngoingTimeoutForType("actor2"))
	assert.False(t, config.GetDrainRebalancedActorsForType("actor2"))
	assert.False(t, config.coreConfig.GetReentrancyForType("actor2").Enabled)
	assert.Equal(t, 0, config.coreConfig.GetRemindersPartitionCountForType("actor2"))

	assert.Equal(t, time.Second*5, config.GetIdleTimeoutForType("actor3"))
	assert.Equal(t, time.Second, config.GetDrainOngoingTimeoutForType("actor3"))
	assert.True(t, config.GetDrainRebalancedActorsForType("actor3"))
	assert.True(t, config.coreConfig.GetReentrancyForType("actor3").Enabled)
	assert.Equal(t, 10, config.coreConfig.GetRemindersPartitionCountForType("actor3"))

	assert.Equal(t, time.Second, config.GetIdleTimeoutForType("actor4"))
	assert.Equal(t, time.Second*5, config.GetDrainOngoingTimeoutForType("actor4"))
	assert.True(t, config.GetDrainRebalancedActorsForType("actor4"))
	assert.False(t, config.coreConfig.GetReentrancyForType("actor4").Enabled)
	assert.Equal(t, 1, config.coreConfig.GetRemindersPartitionCountForType("actor4"))
}

func TestOnlyHostedActorTypesAreIncluded(t *testing.T) {
	appConfig := appConfig.ApplicationConfig{
		Entities:                   []string{"actor1", "actor2"},
		ActorIdleTimeout:           "1s",
		ActorScanInterval:          "2s",
		DrainOngoingCallTimeout:    "5s",
		DrainRebalancedActors:      true,
		RemindersStoragePartitions: 1,
		EntityConfigs: []appConfig.EntityConfig{
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
				Reentrancy: appConfig.ReentrancyConfig{
					Enabled: true,
				},
				RemindersStoragePartitions: 10,
			},
		},
	}

	config := NewConfig(ConfigOpts{
		HostAddress:        HostAddress,
		AppID:              AppID,
		PlacementAddresses: []string{PlacementAddress},
		Port:               Port,
		Namespace:          Namespace,
		AppConfig:          appConfig,
	})

	assert.Contains(t, config.coreConfig.EntityConfigs, "actor1")
	assert.Contains(t, config.coreConfig.EntityConfigs, "actor2")
	assert.NotContains(t, config.coreConfig.EntityConfigs, "actor3")
}
