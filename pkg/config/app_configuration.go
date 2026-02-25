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

package config

// ApplicationConfig is an optional config supplied by user code.
type ApplicationConfig struct {
	Entities []string `json:"entities"`
	// Duration. example: "1h".
	ActorIdleTimeout string `json:"actorIdleTimeout"`
	// Duration. example: "30s".
	DrainOngoingCallTimeout string           `json:"drainOngoingCallTimeout"`
	DrainRebalancedActors   *bool            `json:"drainRebalancedActors"`
	Reentrancy              ReentrancyConfig `json:"reentrancy,omitempty"`

	// DEPRECATED.
	RemindersStoragePartitions int `json:"remindersStoragePartitions"`

	// Duplicate of the above config so we can assign it to individual entities.
	EntityConfigs []EntityConfig `json:"entitiesConfig,omitempty"`
}

type ReentrancyConfig struct {
	Enabled       bool `json:"enabled"`
	MaxStackDepth *int `json:"maxStackDepth,omitempty"`
}

type EntityConfig struct {
	Entities []string `json:"entities"`
	// Duration. example: "1h".
	ActorIdleTimeout string `json:"actorIdleTimeout"`
	// Duration. example: "30s".
	DrainOngoingCallTimeout string           `json:"drainOngoingCallTimeout"`
	DrainRebalancedActors   *bool            `json:"drainRebalancedActors"`
	Reentrancy              ReentrancyConfig `json:"reentrancy,omitempty"`

	// DEPRECATED.
	RemindersStoragePartitions int `json:"remindersStoragePartitions"`
}
