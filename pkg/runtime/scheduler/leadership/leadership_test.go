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

package leadership

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestSet(t *testing.T) {
	t.Parallel()

	l := New()
	leader, unsupported, ch := l.Leader()
	assert.Empty(t, leader)
	assert.False(t, unsupported)
	assert.False(t, closed(ch))

	l.Set("10.0.0.1:50006")
	assert.True(t, closed(ch), "a leader change must signal waiters")
	leader, unsupported, ch = l.Leader()
	assert.Equal(t, "10.0.0.1:50006", leader)
	assert.False(t, unsupported)

	// An identical leader is not a change.
	l.Set("10.0.0.1:50006")
	assert.False(t, closed(ch))

	l.Set("")
	assert.True(t, closed(ch), "losing the leader must signal waiters")
	leader, _, ch = l.Leader()
	assert.Empty(t, leader)

	l.Set("10.0.0.2:50006")
	assert.True(t, closed(ch))
	leader, _, _ = l.Leader()
	assert.Equal(t, "10.0.0.2:50006", leader)
}

func TestSetUnsupported(t *testing.T) {
	t.Parallel()

	l := New()
	l.Set("10.0.0.1:50006")
	_, _, ch := l.Leader()

	l.SetUnsupported()
	assert.True(t, closed(ch))
	leader, unsupported, ch := l.Leader()
	assert.Empty(t, leader, "an unsupported cluster has no leader")
	assert.True(t, unsupported)

	// Repeating unsupported is not a change.
	l.SetUnsupported()
	assert.False(t, closed(ch))

	// A later broadcast with no leader still clears unsupported.
	l.Set("")
	assert.True(t, closed(ch))
	leader, unsupported, _ = l.Leader()
	assert.Empty(t, leader)
	assert.False(t, unsupported)
}
