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

package backoff

import (
	"math/rand/v2"
	"time"
)

// Jitter is decorrelated exponential jitter (next = uniform(base, min(cap,
// prev*3))): under overload, concurrent retries spread out instead of
// re-colliding on a fixed interval.
type Jitter struct {
	base time.Duration
	cap  time.Duration
	prev time.Duration
}

func NewJitter(base, cap time.Duration) *Jitter {
	return &Jitter{
		base: base,
		cap:  cap,
		prev: base,
	}
}

func (j *Jitter) Reset() {
	j.prev = j.base
}

func (j *Jitter) NextBackOff() time.Duration {
	hi := min(j.prev*3, j.cap)
	next := j.base
	if hi > j.base {
		next += rand.N(hi - j.base) //nolint:gosec // retry jitter, not security sensitive
	}
	j.prev = next
	return next
}
