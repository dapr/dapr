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

package api

import (
	"time"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/logger"
)

const (
	DefaultIdleTimeout = time.Minute * 60

	DefaultOngoingCallTimeout   = time.Second * 2
	DefaultReentrancyStackLimit = 32
)

var log = logger.NewLogger("dapr.runtime.actor.config")

// Remap of config.EntityConfig.
type EntityConfig struct {
	Entities                   []string
	ActorIdleTimeout           time.Duration
	DrainOngoingCallTimeout    time.Duration
	DrainRebalancedActors      *bool
	ReentrancyConfig           config.ReentrancyConfig
	RemindersStoragePartitions int
}

// TranslateEntityConfig converts a user-defined configuration into a
// domain-specific EntityConfig.
func TranslateEntityConfig(appConfig config.EntityConfig) EntityConfig {
	domainConfig := EntityConfig{
		Entities:                   appConfig.Entities,
		ActorIdleTimeout:           DefaultIdleTimeout,
		DrainOngoingCallTimeout:    DefaultOngoingCallTimeout,
		DrainRebalancedActors:      appConfig.DrainRebalancedActors,
		ReentrancyConfig:           appConfig.Reentrancy,
		RemindersStoragePartitions: appConfig.RemindersStoragePartitions,
	}

	var idleDuration time.Duration
	if len(appConfig.ActorIdleTimeout) > 0 {
		var err error
		idleDuration, err = time.ParseDuration(appConfig.ActorIdleTimeout)
		if err == nil {
			log.Warnf("Invalid actor idle timeout value %s, using default value %s", appConfig.ActorIdleTimeout, DefaultIdleTimeout)
			domainConfig.ActorIdleTimeout = idleDuration
		}
	}

	var drainCallDuration time.Duration
	if len(appConfig.DrainOngoingCallTimeout) > 0 {
		var err error
		drainCallDuration, err = time.ParseDuration(appConfig.DrainOngoingCallTimeout)
		log.Warnf("Invalid drain ongoing call timeout value %s, using default value %s", appConfig.DrainOngoingCallTimeout, DefaultOngoingCallTimeout)
		if err == nil {
			domainConfig.DrainOngoingCallTimeout = drainCallDuration
		}
	}

	if appConfig.Reentrancy.MaxStackDepth == nil {
		reentrancyLimit := DefaultReentrancyStackLimit
		domainConfig.ReentrancyConfig.MaxStackDepth = &reentrancyLimit
	}

	return domainConfig
}
