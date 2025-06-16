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

package healthz

import (
	"sync"
)

// Healthz implements a health check mechanism.
type Healthz interface {
	IsReady() bool
	GetUnhealthyTargets() []string
	AddTarget(name string) Target
}

type healthz struct {
	mu        sync.RWMutex
	targets   int64
	unhealthy map[string]struct{}
}

func New() Healthz {
	return &healthz{
		unhealthy: make(map[string]struct{}),
		targets:   0,
	}
}

func (h *healthz) IsReady() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	// If no targets are registered, the server is not ready.
	return h.targets > 0 && len(h.unhealthy) == 0
}

func (h *healthz) GetUnhealthyTargets() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	unhealthyList := make([]string, 0, len(h.unhealthy))
	for name := range h.unhealthy {
		unhealthyList = append(unhealthyList, name)
	}
	return unhealthyList
}

func (h *healthz) AddTarget(name string) Target {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := &target{
		name:    name,
		healthz: h,
	}
	h.targets++
	h.unhealthy[name] = struct{}{}
	return t
}

func (h *healthz) markHealthy(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.unhealthy, name)
}

func (h *healthz) markUnhealthy(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unhealthy[name] = struct{}{}
}
