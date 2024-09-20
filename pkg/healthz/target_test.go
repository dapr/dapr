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
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_target_interface(t *testing.T) {
	var _ Target = new(target)
}

func Test_target(t *testing.T) {
	h := New().(*healthz)

	assert.Equal(t, int64(0), h.targets.Load())
	assert.Equal(t, int64(0), h.targetsReady.Load())
	assert.False(t, h.IsReady())

	ta1 := h.AddTarget().(*target)
	assert.Same(t, h, ta1.healthz)
	assert.Equal(t, int64(1), h.targets.Load())
	assert.Equal(t, int64(0), h.targetsReady.Load())
	assert.False(t, h.IsReady())

	ta1.NotReady()
	assert.Same(t, h, ta1.healthz)
	assert.Equal(t, int64(1), h.targets.Load())
	assert.Equal(t, int64(0), h.targetsReady.Load())
	assert.False(t, h.IsReady())

	ta2 := h.AddTarget().(*target)
	assert.Same(t, h, ta2.healthz)
	assert.Equal(t, int64(2), h.targets.Load())
	assert.Equal(t, int64(0), h.targetsReady.Load())
	assert.False(t, h.IsReady())

	ta1.Ready()
	assert.Equal(t, int64(2), h.targets.Load())
	assert.Equal(t, int64(1), h.targetsReady.Load())
	assert.False(t, h.IsReady())

	ta1.Ready()
	assert.Equal(t, int64(2), h.targets.Load())
	assert.Equal(t, int64(1), h.targetsReady.Load())
	assert.False(t, h.IsReady())

	ta2.Ready()
	assert.Equal(t, int64(2), h.targets.Load())
	assert.Equal(t, int64(2), h.targetsReady.Load())
	assert.True(t, h.IsReady())

	ta2.Ready()
	assert.Equal(t, int64(2), h.targets.Load())
	assert.Equal(t, int64(2), h.targetsReady.Load())
	assert.True(t, h.IsReady())

	ta1.NotReady()
	assert.Equal(t, int64(2), h.targets.Load())
	assert.Equal(t, int64(1), h.targetsReady.Load())
	assert.False(t, h.IsReady())

	ta1.NotReady()
	assert.Equal(t, int64(2), h.targets.Load())
	assert.Equal(t, int64(1), h.targetsReady.Load())
	assert.False(t, h.IsReady())

	ta2.Ready()
	assert.Equal(t, int64(2), h.targets.Load())
	assert.Equal(t, int64(1), h.targetsReady.Load())
	assert.False(t, h.IsReady())

	ta2.NotReady()
	assert.Equal(t, int64(2), h.targets.Load())
	assert.Equal(t, int64(0), h.targetsReady.Load())
	assert.False(t, h.IsReady())
}
