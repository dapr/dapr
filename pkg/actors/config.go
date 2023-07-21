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

	"golang.org/x/exp/maps"

	daprAppConfig "github.com/dapr/dapr/pkg/config"
)

// Config is the actor runtime configuration.
type Config struct {
	HostAddress                   string
	AppID                         string
	PlacementAddresses            []string
	HostedActorTypes              hostedActors
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
	c := Config{
		HostAddress:                   opts.HostAddress,
		AppID:                         opts.AppID,
		PlacementAddresses:            opts.PlacementAddresses,
		Port:                          opts.Port,
		Namespace:                     opts.Namespace,
		DrainRebalancedActors:         opts.AppConfig.DrainRebalancedActors,
		HostedActorTypes:              NewHostedActors(opts.AppConfig.Entities),
		Reentrancy:                    opts.AppConfig.Reentrancy,
		RemindersStoragePartitions:    opts.AppConfig.RemindersStoragePartitions,
		HealthHTTPClient:              opts.HealthHTTPClient,
		HealthEndpoint:                opts.HealthEndpoint,
		HeartbeatInterval:             defaultHeartbeatInterval,
		ActorDeactivationScanInterval: defaultActorScanInterval,
		ActorIdleTimeout:              defaultActorIdleTimeout,
		DrainOngoingCallTimeout:       defaultOngoingCallTimeout,
		EntityConfigs:                 make(map[string]EntityConfig),
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

	return c
}

func (c *Config) GetIdleTimeoutForType(actorType string) time.Duration {
	if val, ok := c.EntityConfigs[actorType]; ok {
		return val.ActorIdleTimeout
	}
	return c.ActorIdleTimeout
}

func (c *Config) GetDrainOngoingTimeoutForType(actorType string) time.Duration {
	if val, ok := c.EntityConfigs[actorType]; ok {
		return val.DrainOngoingCallTimeout
	}
	return c.DrainOngoingCallTimeout
}

func (c *Config) GetDrainRebalancedActorsForType(actorType string) bool {
	if val, ok := c.EntityConfigs[actorType]; ok {
		return val.DrainRebalancedActors
	}
	return c.DrainRebalancedActors
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

func translateEntityConfig(appConfig daprAppConfig.EntityConfig) EntityConfig {
	domainConfig := EntityConfig{
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

type hostedActors map[string]struct{}

// NewHostedActors creates a new hostedActors from a slice of actor types.
func NewHostedActors(actorTypes []string) hostedActors {
	// Add + 1 capacity because there's likely the built-in actor engine
	ha := make(hostedActors, len(actorTypes)+1)
	for _, at := range actorTypes {
		ha[at] = struct{}{}
	}
	return ha
}

// AddActorType adds an actor type.
func (ha hostedActors) AddActorType(actorType string) {
	ha[actorType] = struct{}{}
}

// IsActorTypeHosted returns true if the actor type is hosted.
func (ha hostedActors) IsActorTypeHosted(actorType string) bool {
	_, ok := ha[actorType]
	return ok
}

// ListActorTypes returns a slice of hosted actor types (in indeterminate order).
func (ha hostedActors) ListActorTypes() []string {
	return maps.Keys(ha)
}
