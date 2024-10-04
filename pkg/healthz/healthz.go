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
	"sync/atomic"
)

// Healthz implements a health check mechanism.
// Tagets can be added to the health check, and the health check will return
// true when all targets are ready.
// Returns not ready if no targets are registered.
type Healthz interface {
	IsReady() bool
	AddTarget() Target
	AddTargetSet(int) []Target
}

type healthz struct {
	targetsReady atomic.Int64
	targets      atomic.Int64
}

func New() Healthz {
	return new(healthz)
}

func (h *healthz) IsReady() bool {
	targets := h.targets.Load()
	// If no targets are registered, the server is not ready.
	return targets != 0 && targets == h.targetsReady.Load()
}

func (h *healthz) AddTarget() Target {
	h.targets.Add(1)
	return &target{healthz: h}
}

func (h *healthz) AddTargetSet(n int) []Target {
	targets := make([]Target, n)
	for i := range n {
		targets[i] = h.AddTarget()
	}
	return targets
}
