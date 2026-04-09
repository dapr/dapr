/*
Copyright 2026 The Dapr Authors
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

package connections

import (
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

const maxPendingPerGate = 100_000

// concurrencyGate enforces a local concurrency limit for a given gate key.
// Each scheduler enforces globalLimit / schedulerCount as its local share.
type concurrencyGate struct {
	globalLimit uint32
	localLimit  uint32
	current     uint32
	pending     []*loops.TriggerRequest
}

func newConcurrencyGate(globalLimit, schedulerCount uint32) *concurrencyGate {
	return &concurrencyGate{
		globalLimit: globalLimit,
		localLimit:  localLimitFromGlobal(globalLimit, schedulerCount),
	}
}

func localLimitFromGlobal(globalLimit, schedulerCount uint32) uint32 {
	if schedulerCount <= 1 {
		return globalLimit
	}
	local := globalLimit / schedulerCount
	if local < 1 {
		return 1
	}
	return local
}

func (g *concurrencyGate) tryAcquire() bool {
	if g.current < g.localLimit {
		g.current++
		return true
	}
	return false
}

func (g *concurrencyGate) release() {
	if g.current > 0 {
		g.current--
	}
}

func (g *concurrencyGate) enqueue(req *loops.TriggerRequest) bool {
	if len(g.pending) >= maxPendingPerGate {
		return false
	}
	g.pending = append(g.pending, req)
	return true
}

func (g *concurrencyGate) dequeue() *loops.TriggerRequest {
	if len(g.pending) == 0 {
		return nil
	}
	req := g.pending[0]
	g.pending[0] = nil
	g.pending = g.pending[1:]
	return req
}

func (g *concurrencyGate) recalculateLocal(schedulerCount uint32) {
	g.localLimit = localLimitFromGlobal(g.globalLimit, schedulerCount)
}

func (g *concurrencyGate) pendingLen() int {
	return len(g.pending)
}
