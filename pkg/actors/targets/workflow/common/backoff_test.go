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

package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_JitterBackoff(t *testing.T) {
	t.Parallel()

	j := NewJitterBackoff(RetryBackoffBase, RetryBackoffCap)

	prev := RetryBackoffBase
	for range 100 {
		d := j.NextBackOff()
		assert.GreaterOrEqual(t, d, RetryBackoffBase)
		assert.Less(t, d, RetryBackoffCap)
		// Decorrelated growth: each draw is bounded by three times the
		// previous draw (capped), never more.
		assert.Less(t, d, min(prev*3, RetryBackoffCap)+1)
		prev = d
	}

	j.Reset()
	first := j.NextBackOff()
	assert.GreaterOrEqual(t, first, RetryBackoffBase)
	assert.Less(t, first, 3*RetryBackoffBase)
}

func Test_RetryForeverPolicy(t *testing.T) {
	t.Parallel()

	seen := make(map[time.Duration]struct{})
	for range 50 {
		policy := RetryForeverPolicy()
		constant := policy.GetConstant()
		require.NotNil(t, constant)
		assert.Nil(t, constant.MaxRetries)

		interval := constant.GetInterval().AsDuration()
		assert.GreaterOrEqual(t, interval, RetryBackoffBase)
		assert.Less(t, interval, RetryBackoffCap)
		seen[interval] = struct{}{}
	}

	// Draws must actually decorrelate across creates.
	assert.Greater(t, len(seen), 1)
}
