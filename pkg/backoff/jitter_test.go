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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_Jitter(t *testing.T) {
	t.Parallel()

	const (
		base = 50 * time.Millisecond
		cap  = 2 * time.Second
	)

	j := NewJitter(base, cap)

	prev := base
	for range 100 {
		d := j.NextBackOff()
		assert.GreaterOrEqual(t, d, base)
		assert.Less(t, d, cap)
		// Decorrelated growth: each draw is bounded by three times the
		// previous draw (capped), never more.
		assert.Less(t, d, min(prev*3, cap)+1)
		prev = d
	}

	j.Reset()
	first := j.NextBackOff()
	assert.GreaterOrEqual(t, first, base)
	assert.Less(t, first, 3*base)
}
