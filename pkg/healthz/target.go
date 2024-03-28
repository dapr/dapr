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

// Target determines the health of a target. Initial state is not ready.
type Target interface {
	Ready()
	NotReady()
}

type target struct {
	healthz *healthz
	ready   atomic.Bool
}

func (t *target) Ready() {
	if t.ready.CompareAndSwap(false, true) {
		t.healthz.targetsReady.Add(1)
	}
}

func (t *target) NotReady() {
	if t.ready.CompareAndSwap(true, false) {
		t.healthz.targetsReady.Add(-1)
	}
}
