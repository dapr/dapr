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
	"net/http"
	"time"

	core "github.com/dapr/dapr/pkg/actors/core"

	daprAppConfig "github.com/dapr/dapr/pkg/config"
)

type Config struct {
	coreConfig core.Config
}

const (
	defaultActorIdleTimeout     = time.Minute * 60
	defaultHeartbeatInterval    = time.Second * 1
	defaultActorScanInterval    = time.Second * 30
	defaultOngoingCallTimeout   = time.Second * 60
	defaultReentrancyStackLimit = 32
)

// ConfigOpts contains options for NewConfig.
type ConfigOpts struct {
	HostAddress        string
	AppID              string
	PlacementAddresses []string
	Port               int
	Namespace          string
	AppConfig          daprAppConfig.ApplicationConfig
	HealthHTTPClient   *http.Client
	HealthEndpoint     string
	AppChannelAddress  string
	PodName            string
}

// NewConfig returns the actor runtime configuration.
func NewConfig(opts ConfigOpts) Config {
	c := core.Config{
		HostAddress:                   opts.HostAddress,
		AppID:                         opts.AppID,
		PlacementAddresses:            opts.PlacementAddresses,
		Port:                          opts.Port,
		Namespace:                     opts.Namespace,
		DrainRebalancedActors:         opts.AppConfig.DrainRebalancedActors,
		HostedActorTypes:              opts.AppConfig.Entities,
		Reentrancy:                    opts.AppConfig.Reentrancy,
		RemindersStoragePartitions:    opts.AppConfig.RemindersStoragePartitions,
		HealthHTTPClient:              opts.HealthHTTPClient,
		HealthEndpoint:                opts.HealthEndpoint,
		HeartbeatInterval:             defaultHeartbeatInterval,
		ActorDeactivationScanInterval: defaultActorScanInterval,
		ActorIdleTimeout:              defaultActorIdleTimeout,
		DrainOngoingCallTimeout:       defaultOngoingCallTimeout,
		EntityConfigs:                 make(map[string]core.EntityConfig),
		AppChannelAddress:             opts.AppChannelAddress,
		PodName:                       opts.PodName,
	}

	scanDuration, err := time.ParseDuration(opts.AppConfig.ActorScanInterval)
	if err == nil {
		c.ActorDeactivationScanInterval = scanDuration
	}

	idleDuration, err := time.ParseDuration(opts.AppConfig.ActorIdleTimeout)
	if err == nil {
		c.ActorIdleTimeout = idleDuration
	}

	drainCallDuration, err := time.ParseDuration(opts.AppConfig.DrainOngoingCallTimeout)
	if err == nil {
		c.DrainOngoingCallTimeout = drainCallDuration
	}

	if opts.AppConfig.Reentrancy.MaxStackDepth == nil {
		reentrancyLimit := defaultReentrancyStackLimit
		c.Reentrancy.MaxStackDepth = &reentrancyLimit
	}

	// Make a map of the hosted actors so we can reference it below.
	hostedTypes := make(map[string]bool, len(opts.AppConfig.Entities))
	for _, hostedType := range opts.AppConfig.Entities {
		hostedTypes[hostedType] = true
	}

	for _, entityConfg := range opts.AppConfig.EntityConfigs {
		config := translateEntityConfig(entityConfg)
		for _, entity := range entityConfg.Entities {
			if _, ok := hostedTypes[entity]; ok {
				c.EntityConfigs[entity] = config
			} else {
				log.Warnf("Configuration specified for non-hosted actor type: %s", entity)
			}
		}
	}
	Config := Config{coreConfig: c}
	return Config
}

func (c *Config) GetIdleTimeoutForType(actorType string) time.Duration {
	if val, ok := c.coreConfig.EntityConfigs[actorType]; ok {
		return val.ActorIdleTimeout
	}
	return c.coreConfig.ActorIdleTimeout
}

func (c *Config) GetDrainOngoingTimeoutForType(actorType string) time.Duration {
	if val, ok := c.coreConfig.EntityConfigs[actorType]; ok {
		return val.DrainOngoingCallTimeout
	}
	return c.coreConfig.DrainOngoingCallTimeout
}

func (c *Config) GetDrainRebalancedActorsForType(actorType string) bool {
	if val, ok := c.coreConfig.EntityConfigs[actorType]; ok {
		return val.DrainRebalancedActors
	}
	return c.coreConfig.DrainRebalancedActors
}

// func (c *Config) GetRemindersPartitionCountForType(actorType string) int {
// 	if val, ok := c.EntityConfigs[actorType]; ok {
// 		return val.RemindersStoragePartitions
// 	}
// 	return c.RemindersStoragePartitions
// }

func translateEntityConfig(appConfig daprAppConfig.EntityConfig) core.EntityConfig {
	domainConfig := core.EntityConfig{
		Entities:                   appConfig.Entities,
		ActorIdleTimeout:           defaultActorIdleTimeout,
		DrainOngoingCallTimeout:    defaultOngoingCallTimeout,
		DrainRebalancedActors:      appConfig.DrainRebalancedActors,
		ReentrancyConfig:           appConfig.Reentrancy,
		RemindersStoragePartitions: appConfig.RemindersStoragePartitions,
	}

	idleDuration, err := time.ParseDuration(appConfig.ActorIdleTimeout)
	if err == nil {
		domainConfig.ActorIdleTimeout = idleDuration
	}

	drainCallDuration, err := time.ParseDuration(appConfig.DrainOngoingCallTimeout)
	if err == nil {
		domainConfig.DrainOngoingCallTimeout = drainCallDuration
	}

	if appConfig.Reentrancy.MaxStackDepth == nil {
		reentrancyLimit := defaultReentrancyStackLimit
		domainConfig.ReentrancyConfig.MaxStackDepth = &reentrancyLimit
	}

	return domainConfig
}
