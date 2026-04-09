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
	current     uint32
	pending     []*loops.TriggerRequest
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

func (g *concurrencyGate) tryAcquire(schedulerCount uint32) bool {
	if g.current < localLimitFromGlobal(g.globalLimit, schedulerCount) {
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

	if len(g.pending) == 0 {
		g.pending = nil
	} else if cap(g.pending) > 64 && len(g.pending)*4 <= cap(g.pending) {
		pending := make([]*loops.TriggerRequest, len(g.pending))
		copy(pending, g.pending)
		g.pending = pending
	}

	return req
}

func (g *concurrencyGate) pendingLen() int {
	return len(g.pending)
}
