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

func Test_healthz_interface(t *testing.T) {
	var _ Healthz = new(healthz)
	var _ Healthz = New()
}

func Test_healthz(t *testing.T) {
	healthz := New()
	assert.False(t, healthz.IsReady())

	target1 := healthz.AddTarget()
	assert.False(t, healthz.IsReady())
	target1.Ready()
	assert.True(t, healthz.IsReady())
	target2 := healthz.AddTarget()
	assert.False(t, healthz.IsReady())
	target2.Ready()
	assert.True(t, healthz.IsReady())
	target1.NotReady()
	assert.False(t, healthz.IsReady())
	target2.NotReady()
	assert.False(t, healthz.IsReady())
	target1.Ready()
	assert.False(t, healthz.IsReady())
	target2.Ready()
	assert.True(t, healthz.IsReady())

	targetSet := healthz.AddTargetSet(3)
	assert.False(t, healthz.IsReady())
	targetSet[0].Ready()
	assert.False(t, healthz.IsReady())
	targetSet[1].Ready()
	assert.False(t, healthz.IsReady())
	targetSet[2].Ready()
	assert.True(t, healthz.IsReady())
}
