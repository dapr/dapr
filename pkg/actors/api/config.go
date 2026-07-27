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
	"strings"
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
// domain-specific EntityConfig. disseminationTimeout is the daprd-side
// placement dissemination timeout used to clamp the per-entity drain
// timeout via ClampDrainOngoingCallTimeout; pass <= 0 to disable clamping.
func TranslateEntityConfig(appConfig config.EntityConfig, disseminationTimeout time.Duration) EntityConfig {
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
			clamped := ClampDrainOngoingCallTimeout(drainCallDuration, disseminationTimeout, "entities="+joinEntities(appConfig.Entities))
			domainConfig.DrainOngoingCallTimeout = &clamped
		}
	}

	if appConfig.Reentrancy.MaxStackDepth == nil {
		reentrancyLimit := DefaultReentrancyStackLimit
		domainConfig.ReentrancyConfig.MaxStackDepth = &reentrancyLimit
	}

	return domainConfig
}

// ClampDrainOngoingCallTimeout returns drain unchanged when it is shorter
// than the placement dissemination timeout. If drain is greater than or
// equal to disseminationTimeout, it logs a warning and returns
// disseminationTimeout * drainTimeoutBudgetRatio, floored at
// DefaultOngoingCallTimeout. This prevents a long drain from holding a
// placement LOCK -> UPDATE -> UNLOCK round past the dissemination
// timeout, which would reset the placement stream.
//
// disseminationTimeout <= 0 disables the clamp.
func ClampDrainOngoingCallTimeout(drain, disseminationTimeout time.Duration, source string) time.Duration {
	if disseminationTimeout <= 0 || drain < disseminationTimeout {
		return drain
	}

	clamped := max(time.Duration(float64(disseminationTimeout)*drainTimeoutBudgetRatio), DefaultOngoingCallTimeout)
	log.Warnf("drainOngoingCallTimeout (%s) for %s meets or exceeds the dissemination timeout (%s); clamping to %s to avoid blocking placement dissemination",
		drain, source, disseminationTimeout, clamped)
	return clamped
}

func joinEntities(entities []string) string {
	if len(entities) == 0 {
		return "<none>"
	}
	return strings.Join(entities, ",")
}
