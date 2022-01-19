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
	"time"

	app_config "github.com/dapr/dapr/pkg/config"
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
	Reentrancy                    app_config.ReentrancyConfig
	RemindersStoragePartitions    int
}

const (
	defaultActorIdleTimeout     = time.Minute * 60
	defaultHeartbeatInterval    = time.Second * 1
	defaultActorScanInterval    = time.Second * 30
	defaultOngoingCallTimeout   = time.Second * 60
	defaultReentrancyStackLimit = 32
)

// NewConfig returns the actor runtime configuration.
func NewConfig(hostAddress, appID string, placementAddresses []string, hostedActors []string, port int,
	actorScanInterval, actorIdleTimeout, ongoingCallTimeout string, drainRebalancedActors bool, namespace string,
	reentrancy app_config.ReentrancyConfig, remindersStoragePartitions int) Config {
	c := Config{
		HostAddress:                   hostAddress,
		AppID:                         appID,
		PlacementAddresses:            placementAddresses,
		HostedActorTypes:              hostedActors,
		Port:                          port,
		HeartbeatInterval:             defaultHeartbeatInterval,
		ActorDeactivationScanInterval: defaultActorScanInterval,
		ActorIdleTimeout:              defaultActorIdleTimeout,
		DrainOngoingCallTimeout:       defaultOngoingCallTimeout,
		DrainRebalancedActors:         drainRebalancedActors,
		Namespace:                     namespace,
		Reentrancy:                    reentrancy,
		RemindersStoragePartitions:    remindersStoragePartitions,
	}

	scanDuration, err := time.ParseDuration(actorScanInterval)
	if err == nil {
		c.ActorDeactivationScanInterval = scanDuration
	}

	idleDuration, err := time.ParseDuration(actorIdleTimeout)
	if err == nil {
		c.ActorIdleTimeout = idleDuration
	}

	drainCallDuration, err := time.ParseDuration(ongoingCallTimeout)
	if err == nil {
		c.DrainOngoingCallTimeout = drainCallDuration
	}

	if reentrancy.MaxStackDepth == nil {
		reentrancyLimit := defaultReentrancyStackLimit
		c.Reentrancy.MaxStackDepth = &reentrancyLimit
	}

	return c
}
