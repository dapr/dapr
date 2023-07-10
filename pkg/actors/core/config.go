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
