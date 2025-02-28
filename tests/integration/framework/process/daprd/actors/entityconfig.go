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

package actors

import (
	"time"

	"github.com/dapr/kit/ptr"
)

type reentrancyEntitiyConfig struct {
	Enabled       bool    `json:"enabled"`
	MaxStackDepth *uint32 `json:"maxStackDepth,omitempty"`
}

type entityConfig struct {
	Entities                []string                 `json:"entities,omitempty"`
	ActorIdleTimeout        *string                  `json:"actorIdleTimeout,omitempty"`
	DrainOngoingCallTimeout *string                  `json:"drainOngoingCallTimeout,omitempty"`
	Reentrancy              *reentrancyEntitiyConfig `json:"reentrancy,omitempty"`
}

type EntityConfig func(*entityConfig)

func WithEntityConfigEntities(entities ...string) EntityConfig {
	return func(e *entityConfig) {
		e.Entities = append(e.Entities, entities...)
	}
}

func WithEntityConfigActorIdleTimeout(timeout time.Duration) EntityConfig {
	return func(e *entityConfig) {
		e.ActorIdleTimeout = ptr.Of(timeout.String())
	}
}

func WithEntityConfigDrainOngoingCallTimeout(timeout time.Duration) EntityConfig {
	return func(e *entityConfig) {
		e.DrainOngoingCallTimeout = ptr.Of(timeout.String())
	}
}

func WithEntityConfigReentrancy(enabled bool, maxDepth *uint32) EntityConfig {
	return func(e *entityConfig) {
		e.Reentrancy = &reentrancyEntitiyConfig{
			Enabled:       enabled,
			MaxStackDepth: maxDepth,
		}
	}
}
