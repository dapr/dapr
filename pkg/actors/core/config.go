/*
Copyright 2023 The Dapr Authors
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

package core

import (
	"net/http"
	"time"

	daprAppConfig "github.com/dapr/dapr/pkg/config"
)

// Config is the actor runtime configuration.
type Config struct {
	HostAddress                   string
	AppID                         string
	PlacementAddresses            []string
	HostedActorTypes              []string
	Port                          int
	HeartbeatInterval             time.Duration
	ActorDeactivationScanInterval time.Duration
	ActorIdleTimeout              time.Duration
	DrainOngoingCallTimeout       time.Duration
	DrainRebalancedActors         bool
	Namespace                     string
	Reentrancy                    daprAppConfig.ReentrancyConfig
	RemindersStoragePartitions    int
	EntityConfigs                 map[string]EntityConfig
	HealthHTTPClient              *http.Client
	HealthEndpoint                string
	AppChannelAddress             string
	PodName                       string
}

// Remap of daprAppConfig.EntityConfig but with more useful types for actors.go.
type EntityConfig struct {
	Entities                   []string
	ActorIdleTimeout           time.Duration
	DrainOngoingCallTimeout    time.Duration
	DrainRebalancedActors      bool
	ReentrancyConfig           daprAppConfig.ReentrancyConfig
	RemindersStoragePartitions int
}

func (c *Config) GetReentrancyForType(actorType string) daprAppConfig.ReentrancyConfig {
	if val, ok := c.EntityConfigs[actorType]; ok {
		return val.ReentrancyConfig
	}
	return c.Reentrancy
}

func (c *Config) GetRemindersPartitionCountForType(actorType string) int {
	if val, ok := c.EntityConfigs[actorType]; ok {
		return val.RemindersStoragePartitions
	}
	return c.RemindersStoragePartitions
}
