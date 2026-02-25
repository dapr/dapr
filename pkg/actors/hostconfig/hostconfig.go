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

package hostconfig

import (
	"sync"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
)

type Config struct {
	AppChannel              channel.AppChannel
	EntityConfigs           []config.EntityConfig
	DrainRebalancedActors   *bool
	DrainOngoingCallTimeout string
	HostedActorTypes        []string
	DefaultIdleTimeout      string
	Reentrancy              config.ReentrancyConfig
}

type HostConfig struct {
	lock   sync.RWMutex
	config Config
	set    bool
}

func New() *HostConfig {
	return new(HostConfig)
}

func (h *HostConfig) Set(config Config) {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.config = config
	h.set = true
}

func (h *HostConfig) Get() (Config, bool) {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.config, h.set
}
