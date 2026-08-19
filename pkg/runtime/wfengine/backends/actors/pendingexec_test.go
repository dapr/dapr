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

package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_activityExecutions(t *testing.T) {
	t.Parallel()

	a := newActivityExecutions()
	key := activityExecutionKey("wf1", 3)

	assert.False(t, a.heldFor("wf1", 3))

	release := a.add(key)
	assert.True(t, a.heldFor("wf1", 3))
	assert.False(t, a.heldFor("wf1", 4))
	assert.False(t, a.heldFor("wf2", 3))

	// Overlapping registrations for the same execution key are counted.
	release2 := a.add(key)
	release()
	assert.True(t, a.heldFor("wf1", 3))
	release2()
	assert.False(t, a.heldFor("wf1", 3))

	// The release is idempotent: a delivery and a deregistration both firing
	// must not underflow another registration's count.
	release3 := a.add(key)
	release()
	release2()
	assert.True(t, a.heldFor("wf1", 3))
	release3()
	release3()
	assert.False(t, a.heldFor("wf1", 3))
}
