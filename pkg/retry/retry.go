/*
Copyright 2021 The Dapr Authors
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

package retry

import (
	"math/rand"
	"time"
)

const (
	DefaultLinearBackoffInterval = time.Second
	DefaultLinearRetryCount      = 3
)

// Jitter returns a random duration in the range [base-jitter, base+jitter).
// Returns base if jitter is zero or negative.
//
//nolint:gosec
func Jitter(base, jitter time.Duration) time.Duration {
	if jitter <= 0 {
		return base
	}
	return base - jitter + time.Duration(rand.Int63n(int64(jitter*2)))
}
