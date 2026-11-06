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

package retry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_Jitter_ZeroOrNegativeReturnsBase(t *testing.T) {
	t.Parallel()

	const base = 5 * time.Second

	assert.Equal(t, base, Jitter(base, 0))
	assert.Equal(t, base, Jitter(base, -1*time.Second))
}

func Test_Jitter_WithinRange(t *testing.T) {
	t.Parallel()

	const (
		base   = 10 * time.Second
		jitter = 3 * time.Second
	)

	for range 1000 {
		d := Jitter(base, jitter)
		assert.GreaterOrEqual(t, d, base-jitter)
		assert.Less(t, d, base+jitter)
	}
}

func Test_Jitter_LargerThanBase(t *testing.T) {
	t.Parallel()

	const (
		base   = 100 * time.Millisecond
		jitter = 500 * time.Millisecond
	)

	for range 1000 {
		d := Jitter(base, jitter)
		assert.GreaterOrEqual(t, d, base-jitter)
		assert.Less(t, d, base+jitter)
	}
}

func Test_Jitter_Varies(t *testing.T) {
	t.Parallel()

	const (
		base   = 1 * time.Second
		jitter = 1 * time.Second
	)

	first := Jitter(base, jitter)
	varied := false
	for range 100 {
		if Jitter(base, jitter) != first {
			varied = true
			break
		}
	}
	assert.True(t, varied, "expected repeated calls to Jitter to produce different values")
}
