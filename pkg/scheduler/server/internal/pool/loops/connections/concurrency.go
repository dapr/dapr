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

import "time"

// concurrencyGate enforces a local concurrency limit for a given gate key.
// The limit is the smallest of up to three sources: a configured cluster-wide
// limit, the sum of the per-sidecar limits advertised by the gate's hosts,
// and an adaptive limit derived from acknowledgement latency and throughput.
// Cluster-wide limits are divided among schedulers deterministically so that
// the sum equals the limit when the limit >= schedulerCount. A gate with no
// limit from any source is unlimited.
type concurrencyGate struct {
	globalLimit uint32
	hostLimit   uint32
	current     uint32

	// est is nil when the gate has no adaptive limit.
	est *estimator
}

// localLimitFromGlobal computes the local concurrency limit for a scheduler.
// Base slots are distributed evenly; the remainder is assigned to schedulers
// with the lowest indices so the cluster-wide sum equals globalLimit. When
// globalLimit < schedulerCount the share would be zero; we allow 1 everywhere
// to avoid starvation, at the cost of exceeding globalLimit.
func localLimitFromGlobal(globalLimit, schedulerCount, schedulerIdx uint32) uint32 {
	if schedulerCount <= 1 {
		return globalLimit
	}
	base := globalLimit / schedulerCount
	if base < 1 {
		return 1
	}
	if schedulerIdx < globalLimit%schedulerCount {
		return base + 1
	}
	return base
}

// minLimit returns the smaller of two limits, where 0 is unlimited.
func minLimit(a, b uint32) uint32 {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	default:
		return min(a, b)
	}
}

// ceiling is this scheduler's share of the configured and advertised limits.
func (g *concurrencyGate) ceiling(schedulerCount, schedulerIdx uint32) uint32 {
	var limit uint32
	if g.globalLimit > 0 {
		limit = localLimitFromGlobal(g.globalLimit, schedulerCount, schedulerIdx)
	}
	if g.hostLimit > 0 {
		limit = minLimit(limit, localLimitFromGlobal(g.hostLimit, schedulerCount, schedulerIdx))
	}
	return limit
}

// localLimit is the effective limit of the gate on this scheduler, 0 when
// unlimited.
func (g *concurrencyGate) localLimit(schedulerCount, schedulerIdx uint32) uint32 {
	limit := g.ceiling(schedulerCount, schedulerIdx)
	if g.est != nil {
		limit = minLimit(limit, g.est.limit)
	}
	return limit
}

func (g *concurrencyGate) tryAcquire(schedulerCount, schedulerIdx uint32) bool {
	if limit := g.localLimit(schedulerCount, schedulerIdx); limit > 0 && g.current >= limit {
		return false
	}
	g.current++
	return true
}

func (g *concurrencyGate) release() {
	if g.current > 0 {
		g.current--
	}
}

func (g *concurrencyGate) markBound(now time.Time) {
	if g.est != nil {
		g.est.lastBound = now
	}
}

// niceToWeight is the Linux CFS sched_prio_to_weight table, indexed by
// niceness + 20. Each step is a factor of about 1.25.
var niceToWeight = [40]uint64{
	/* -20 */ 88761, 71755, 56483, 46273, 36291,
	/* -15 */ 29154, 23254, 18705, 14949, 11916,
	/* -10 */ 9548, 7620, 6100, 4904, 3906,
	/*  -5 */ 3121, 2501, 1991, 1586, 1277,
	/*   0 */ 1024, 820, 655, 526, 423,
	/*   5 */ 335, 272, 215, 172, 137,
	/*  10 */ 110, 87, 70, 56, 45,
	/*  15 */ 36, 29, 23, 18, 15,
}

// weightOf maps a niceness to its scheduling weight, clamping to [-20, 19].
func weightOf(niceness int32) uint64 {
	return niceToWeight[min(max(niceness, -20), 19)+20]
}
