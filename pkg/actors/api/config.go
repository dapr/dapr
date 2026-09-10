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

	// drainTimeoutBudgetRatio is the fraction of the dissemination timeout that
	// a clamped drain is allowed to consume. The remaining budget is reserved
	// for the non-drain work in a placement LOCK -> UPDATE -> UNLOCK round
	// (HaltNonHosted, sending UPDATE/UNLOCK acks, the placement table swap).
	drainTimeoutBudgetRatio = 0.8
)

var log = logger.NewLogger("dapr.runtime.actor.config")

// EntityDrainConfig is per-actor-type drain configuration; nil fields fall
// back to the global settings.
type EntityDrainConfig struct {
	Timeout               *time.Duration
	DrainRebalancedActors *bool
}

// Remap of config.EntityConfig.
type EntityConfig struct {
	Entities                   []string
	ActorIdleTimeout           time.Duration
	DrainOngoingCallTimeout    *time.Duration
	DrainRebalancedActors      *bool
	ReentrancyConfig           config.ReentrancyConfig
	RemindersStoragePartitions int
}

// TranslateEntityConfig converts a user-defined configuration into a
// domain-specific EntityConfig. Drain timeouts are stored raw; clamping
// happens at drain time.
func TranslateEntityConfig(appConfig config.EntityConfig) EntityConfig {
	domainConfig := EntityConfig{
		Entities:                   appConfig.Entities,
		ActorIdleTimeout:           DefaultIdleTimeout,
		DrainRebalancedActors:      appConfig.DrainRebalancedActors,
		ReentrancyConfig:           appConfig.Reentrancy,
		RemindersStoragePartitions: appConfig.RemindersStoragePartitions,
	}

	if len(appConfig.ActorIdleTimeout) > 0 {
		idleDuration, err := time.ParseDuration(appConfig.ActorIdleTimeout)
		if err != nil {
			log.Warnf("Invalid actor idle timeout value %s, using default value %s", appConfig.ActorIdleTimeout, DefaultIdleTimeout)
		} else {
			domainConfig.ActorIdleTimeout = idleDuration
		}
	}

	if len(appConfig.DrainOngoingCallTimeout) > 0 {
		drainCallDuration, err := time.ParseDuration(appConfig.DrainOngoingCallTimeout)
		if err != nil {
			log.Warnf("Invalid drain ongoing call timeout value %s, using default value %s", appConfig.DrainOngoingCallTimeout, DefaultOngoingCallTimeout)
		} else {
			domainConfig.DrainOngoingCallTimeout = &drainCallDuration
		}
	}

	if appConfig.Reentrancy.MaxStackDepth == nil {
		reentrancyLimit := DefaultReentrancyStackLimit
		domainConfig.ReentrancyConfig.MaxStackDepth = &reentrancyLimit
	}

	return domainConfig
}

// ClampDrainOngoingCallTimeout bounds drain to budget *
// drainTimeoutBudgetRatio, floored at DefaultOngoingCallTimeout, reporting
// whether clamping occurred. Drain below budget passes through; budget <= 0
// disables the clamp.
func ClampDrainOngoingCallTimeout(drain, budget time.Duration) (time.Duration, bool) {
	if budget <= 0 || drain < budget {
		return drain, false
	}

	return max(time.Duration(float64(budget)*drainTimeoutBudgetRatio), DefaultOngoingCallTimeout), true
}
