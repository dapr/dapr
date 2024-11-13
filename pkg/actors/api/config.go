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
)

// Remap of config.EntityConfig.
type EntityConfig struct {
	Entities                   []string
	ActorIdleTimeout           time.Duration
	DrainOngoingCallTimeout    time.Duration
	DrainRebalancedActors      bool
	ReentrancyConfig           config.ReentrancyConfig
	RemindersStoragePartitions int
}
