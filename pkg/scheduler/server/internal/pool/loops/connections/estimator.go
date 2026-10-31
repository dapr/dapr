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
	"math"
	"slices"
	"time"
)

const (
	// minEvalPeriod and maxEvalPeriod bound how long an estimator measures
	// before deciding. The period follows twice the median round trip time,
	// so a probe has time to show its effect on throughput.
	minEvalPeriod = time.Second
	maxEvalPeriod = 30 * time.Second

	// baselineWindows is how many past window medians a name keeps; its
	// baseline is the smallest of them.
	baselineWindows = 60

	// minSamples is how many acknowledgements a name needs in a window to
	// vote, or to contribute to its baseline.
	minSamples = 10

	// maxSamples bounds the round trip times kept per name per window.
	maxSamples = 1024

	// minBaseline stops sub-millisecond baselines from turning noise into
	// overload: a gate is only overloaded once its median round trip time is
	// at least overloadRatio times this.
	minBaseline = 5 * time.Millisecond

	overloadRatio = 2.0
	backoffFactor = 0.75

	// kneeGain scales the in-flight count Little's law gives for the
	// measured throughput at the uncongested round trip time. Above 1 so the
	// app stays busy, below overloadRatio so it is not overloaded.
	kneeGain = 1.5

	probeGain    = 1.25
	probeMinGain = 1.10
	probeHold    = 10 * time.Second
	releaseAfter = 60 * time.Second
)

// estimator derives a concurrency limit from the triggers flowing through a
// gate. While unlimited it watches for overload: the round trip time of each
// voter (a name under an actor type, or an actor type under a namespace)
// compared to its own baseline. Once limited it probes upward while
// throughput follows the limit, backs off on overload, and lets go of the
// limit after it has not been binding for releaseAfter.
type estimator struct {
	// minVoters is how many voters must report overload before the
	// estimator acts.
	minVoters int

	limit     uint32
	prevLimit uint32
	probing   bool
	baseRate  float64

	changedAt time.Time
	holdUntil time.Time
	lastBound time.Time

	windowStart time.Time
	period      time.Duration
	acks        uint32

	// ratio and voters are the latest latency ratio and how many names
	// contributed to it, and serviceTime the typical uncongested round trip
	// time, read by the namespace estimator.
	ratio       float64
	voters      int
	serviceTime time.Duration

	// names holds round trip times per concurrency key. Only actor type
	// estimators record them.
	names map[string]*nameStats
}

type rttSample struct {
	sent time.Time
	rtt  time.Duration
}

type nameStats struct {
	samples []rttSample
	n       int

	medians []time.Duration
	next    int
	idle    int
}

func newEstimator(now time.Time, minVoters int) *estimator {
	return &estimator{
		minVoters:   minVoters,
		changedAt:   now,
		windowStart: now,
		period:      minEvalPeriod,
		names:       make(map[string]*nameStats),
	}
}

func (e *estimator) due(now time.Time) bool {
	return now.Sub(e.windowStart) >= e.period
}

// record adds the round trip time of a successful trigger of the given name.
func (e *estimator) record(name string, sent, now time.Time) {
	ns, ok := e.names[name]
	if !ok {
		ns = new(nameStats)
		e.names[name] = ns
	}
	s := rttSample{sent: sent, rtt: now.Sub(sent)}
	if len(ns.samples) < maxSamples {
		ns.samples = append(ns.samples, s)
	} else {
		ns.samples[ns.n%maxSamples] = s
	}
	ns.n++
}

func (ns *nameStats) baseline() (time.Duration, bool) {
	if len(ns.medians) == 0 {
		return 0, false
	}
	return max(slices.Min(ns.medians), minBaseline), true
}

func (ns *nameStats) pushMedian(m time.Duration) {
	if len(ns.medians) < baselineWindows {
		ns.medians = append(ns.medians, m)
		return
	}
	ns.medians[ns.next] = m
	ns.next = (ns.next + 1) % baselineWindows
}

// latencyRatio is the median over voting names of each name's median round
// trip time divided by its baseline, counting only triggers sent at or after
// cutoff so that samples from before the last limit change do not vote.
func (e *estimator) latencyRatio(cutoff time.Time) (float64, int) {
	var ratios []float64
	for _, ns := range e.names {
		base, ok := ns.baseline()
		if !ok {
			continue
		}
		var rtts []time.Duration
		for _, s := range ns.samples {
			if !s.sent.Before(cutoff) {
				rtts = append(rtts, s.rtt)
			}
		}
		if len(rtts) < minSamples {
			continue
		}
		ratios = append(ratios, float64(median(rtts))/float64(base))
	}
	if len(ratios) == 0 {
		return 0, 0
	}
	return median(ratios), len(ratios)
}

// uncongested is the mean of the names' baselines, weighted by how many
// triggers each name completed this window: the round trip time a trigger
// would take if nothing were queued. Zero when no name has a baseline.
func (e *estimator) uncongested() time.Duration {
	var sum float64
	var weight int
	for _, ns := range e.names {
		base, ok := ns.baseline()
		if !ok || len(ns.samples) == 0 {
			continue
		}
		sum += float64(len(ns.samples)) * float64(base)
		weight += len(ns.samples)
	}
	if weight == 0 {
		return 0
	}
	return time.Duration(sum / float64(weight))
}

// roll closes the window's latency samples, adding each name's median to its
// baseline, and returns the median round trip time across all names.
func (e *estimator) roll() (time.Duration, bool) {
	var total int
	for _, ns := range e.names {
		total += len(ns.samples)
	}
	all := make([]time.Duration, 0, total)
	for name, ns := range e.names {
		rtts := make([]time.Duration, len(ns.samples))
		for i, s := range ns.samples {
			rtts[i] = s.rtt
		}
		all = append(all, rtts...)

		if len(rtts) >= minSamples {
			ns.pushMedian(median(rtts))
			ns.idle = 0
		} else {
			ns.idle++
		}
		ns.samples, ns.n = ns.samples[:0], 0

		if ns.idle > baselineWindows {
			delete(e.names, name)
		}
	}
	if len(all) == 0 {
		return 0, false
	}
	return median(all), true
}

type evalInput struct {
	now      time.Time
	ratio    float64
	voters   int
	inflight uint32
	floor    uint32
	ceiling  uint32

	// serviceTime is the uncongested round trip time, 0 when unknown.
	serviceTime time.Duration
}

// control closes the current window and decides the limit for the next one.
// It reports whether the limit changed, and whether it was created or cut
// because of overload.
func (e *estimator) control(in evalInput) (changed, overload bool) {
	rate := 0.0
	if elapsed := in.now.Sub(e.windowStart).Seconds(); elapsed > 0 {
		rate = float64(e.acks) / elapsed
	}
	bound := !e.lastBound.Before(e.windowStart)
	overloaded := in.voters >= e.minVoters && in.ratio >= overloadRatio
	prev := e.limit

	// By Little's law the pipeline is saturated, without queueing, at its
	// throughput times its uncongested round trip time in flight. Backing off
	// straight to that knee settles in one step where multiplicative
	// decrease alone would take many windows.
	knee := uint32(math.MaxUint32)
	if in.serviceTime > 0 && rate > 0 {
		knee = uint32(min(math.Ceil(kneeGain*rate*in.serviceTime.Seconds()), math.MaxUint32))
	}

	switch {
	case e.limit == 0:
		if overloaded {
			e.limit = clampLimit(min(uint32(backoffFactor*float64(in.inflight)), knee), in.floor, in.ceiling)
			e.holdUntil = in.now.Add(probeHold)
			overload = true
		}
	case e.probing:
		e.probing = false
		if rate < probeMinGain*e.baseRate {
			e.limit = e.prevLimit
			e.holdUntil = in.now.Add(probeHold)
		}
	case overloaded:
		e.limit = clampLimit(min(uint32(backoffFactor*float64(e.limit)), knee), in.floor, in.ceiling)
		e.holdUntil = in.now.Add(probeHold)
		overload = true
	case bound && !in.now.Before(e.holdUntil):
		next := clampLimit(max(e.limit+1, uint32(probeGain*float64(e.limit))), in.floor, in.ceiling)
		if next > e.limit {
			e.prevLimit, e.baseRate, e.probing = e.limit, rate, true
			e.limit = next
		}
	case !bound && in.now.Sub(latest(e.lastBound, e.changedAt)) >= releaseAfter:
		e.limit = 0
	}

	if e.limit != prev {
		e.changedAt = in.now
	}
	e.windowStart = in.now
	e.acks = 0

	return e.limit != prev, overload
}

// clear drops the adaptive limit, as if it had been released.
func (e *estimator) clear(now time.Time) {
	e.limit = 0
	e.probing = false
	e.changedAt = now
}

// clampLimit keeps a derived limit within [max(floor, 1), ceiling], where a
// ceiling of 0 is none.
func clampLimit(limit, floor, ceiling uint32) uint32 {
	limit = max(limit, floor, 1)
	if ceiling > 0 {
		limit = min(limit, ceiling)
	}
	return limit
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func evalPeriod(rtt time.Duration) time.Duration {
	return min(max(2*rtt, minEvalPeriod), maxEvalPeriod)
}

// median returns the lower median, so that with two voters both must be
// slow for the ratio to be.
func median[T time.Duration | float64](vs []T) T {
	slices.Sort(vs)
	return vs[(len(vs)-1)/2]
}
