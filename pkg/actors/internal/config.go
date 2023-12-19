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

package internal

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/exp/maps"

	daprAppConfig "github.com/dapr/dapr/pkg/config"
)

// Config is the actor runtime configuration.
type Config struct {
	HostAddress                   string
	AppID                         string
	ActorsService                 string
	RemindersService              string
	HostedActorTypes              *hostedActors
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

func (c Config) GetRuntimeHostname() string {
	return net.JoinHostPort(c.HostAddress, strconv.Itoa(c.Port))
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

func (c *Config) GetRemindersPartitionCountForType(actorType string) int {
	if val, ok := c.EntityConfigs[actorType]; ok {
		return val.RemindersStoragePartitions
	}
	return c.RemindersStoragePartitions
}

// hostedActors is a thread-safe map of actor types.
// It is optional to specify an idle timeout for an actor type.
// If an idle timeout is not specified, default idle timeout is ought to be used.
type hostedActors struct {
	actors map[string]time.Duration
	lock   sync.RWMutex
}

// NewHostedActors creates a new hostedActors from a slice of actor types.
func NewHostedActors(actorTypes []string) *hostedActors {
	// Add + 1 capacity because there's likely the built-in actor engine
	ha := make(map[string]time.Duration, len(actorTypes)+1)
	for _, at := range actorTypes {
		ha[at] = 0
	}
	return &hostedActors{
		actors: ha,
	}
}

// AddActorType adds an actor type.
func (ha *hostedActors) AddActorType(actorType string, idleTimeout time.Duration) {
	ha.lock.Lock()
	ha.actors[actorType] = idleTimeout
	ha.lock.Unlock()
}

// IsActorTypeHosted returns true if the actor type is hosted.
func (ha *hostedActors) IsActorTypeHosted(actorType string) bool {
	ha.lock.RLock()
	defer ha.lock.RUnlock()
	_, ok := ha.actors[actorType]
	return ok
}

// ListActorTypes returns a slice of hosted actor types (in indeterminate order).
func (ha *hostedActors) ListActorTypes() []string {
	ha.lock.RLock()
	defer ha.lock.RUnlock()
	return maps.Keys(ha.actors)
}

// GetActorIdleTimeout fetches idle timeout stored against an actor type.
func (ha *hostedActors) GetActorIdleTimeout(actorType string) time.Duration {
	ha.lock.RLock()
	defer ha.lock.RUnlock()
	return ha.actors[actorType]
}
