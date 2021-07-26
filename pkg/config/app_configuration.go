// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package config

// ApplicationConfig is an optional config supplied by user code.
type ApplicationConfig struct {
	Entities []string `json:"entities"`
	// Duration. example: "1h"
	ActorIdleTimeout string `json:"actorIdleTimeout"`
	// Duration. example: "30s"
	ActorScanInterval string `json:"actorScanInterval"`
	// Duration. example: "30s"
	DrainOngoingCallTimeout    string           `json:"drainOngoingCallTimeout"`
	DrainRebalancedActors      bool             `json:"drainRebalancedActors"`
	Reentrancy                 ReentrancyConfig `json:"reentrancy,omitempty"`
	RemindersStoragePartitions int              `json:"remindersStoragePartitions"`
}

type ReentrancyConfig struct {
	Enabled       bool `json:"enabled"`
	MaxStackDepth *int `json:"maxStackDepth,omitempty"`
}
