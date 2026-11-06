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
	lock    sync.RWMutex
	targets int64
	// unhealthy maps the name of each unhealthy target to the reason it is
	// not ready. The reason is empty when the target did not set one.
	unhealthy map[string]string
}

func New() Healthz {
	return &healthz{
		unhealthy: make(map[string]string),
		targets:   0,
	}
}

func (h *healthz) IsReady() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	// If no targets are registered, the server is not ready.
	return h.targets > 0 && len(h.unhealthy) == 0
}

// GetUnhealthyTargets returns the name of each unhealthy target. A target
// that set a reason is returned as "name: reason".
func (h *healthz) GetUnhealthyTargets() []string {
	h.lock.RLock()
	defer h.lock.RUnlock()

	unhealthyList := make([]string, 0, len(h.unhealthy))
	for name, reason := range h.unhealthy {
		if reason != "" {
			name += ": " + reason
		}
		unhealthyList = append(unhealthyList, name)
	}
	return unhealthyList
}

func (h *healthz) AddTarget(name string) Target {
	h.lock.Lock()
	defer h.lock.Unlock()
	t := &target{
		name:    name,
		healthz: h,
	}
	h.targets++
	h.unhealthy[name] = ""
	return t
}

func (h *healthz) markHealthy(name string) {
	h.lock.Lock()
	defer h.lock.Unlock()
	delete(h.unhealthy, name)
}

func (h *healthz) markUnhealthy(name string) {
	h.lock.Lock()
	defer h.lock.Unlock()
	h.unhealthy[name] = ""
}

// setNotReadyReason sets the reason of an unhealthy target. It has no effect
// on a healthy target.
func (h *healthz) setNotReadyReason(name, reason string) {
	h.lock.Lock()
	defer h.lock.Unlock()
	if _, ok := h.unhealthy[name]; ok {
		h.unhealthy[name] = reason
	}
}
