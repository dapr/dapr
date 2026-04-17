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

package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeterministicXNSKey_StableForSameInputs(t *testing.T) {
	k1 := DeterministicXNSKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, XNSHopDispatch)
	k2 := DeterministicXNSKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, XNSHopDispatch)
	assert.Equal(t, k1, k2, "same inputs must produce the same key")
	assert.NotEmpty(t, k1)
	assert.Len(t, k1, 64, "SHA-256 hex should be 64 chars")
}

func TestDeterministicXNSKey_ChangesWithEveryField(t *testing.T) {
	base := DeterministicXNSKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, XNSHopDispatch)

	cases := []struct {
		name string
		fn   func() string
	}{
		{"sourceNs", func() string {
			return DeterministicXNSKey("ns-b", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, XNSHopDispatch)
		}},
		{"sourceAppID", func() string {
			return DeterministicXNSKey("ns-a", "app-b", "parent-1", "exec-p1", "child-1", "exec-c1", 7, XNSHopDispatch)
		}},
		{"parentOrchID", func() string {
			return DeterministicXNSKey("ns-a", "app-a", "parent-2", "exec-p1", "child-1", "exec-c1", 7, XNSHopDispatch)
		}},
		{"parentExecID", func() string {
			return DeterministicXNSKey("ns-a", "app-a", "parent-1", "exec-p2", "child-1", "exec-c1", 7, XNSHopDispatch)
		}},
		{"childInstanceID", func() string {
			return DeterministicXNSKey("ns-a", "app-a", "parent-1", "exec-p1", "child-2", "exec-c1", 7, XNSHopDispatch)
		}},
		{"childExecID", func() string {
			return DeterministicXNSKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c2", 7, XNSHopDispatch)
		}},
		{"taskID", func() string {
			return DeterministicXNSKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 8, XNSHopDispatch)
		}},
		{"hop", func() string {
			return DeterministicXNSKey("ns-a", "app-a", "parent-1", "exec-p1", "child-1", "exec-c1", 7, XNSHopResult)
		}},
	}

	seen := map[string]struct{}{base: {}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.fn()
			assert.NotEqual(t, base, got, "changing %s must change the key", c.name)
			// Also assert no collision with any previously-generated variant:
			// separation between fields is what prevents a
			// terminate+purge+rerun from reusing a stale reminder name.
			_, dup := seen[got]
			assert.False(t, dup, "collision on %s", c.name)
			seen[got] = struct{}{}
		})
	}
}

func TestDeterministicXNSKey_FieldSeparation(t *testing.T) {
	// Concatenation without separators would collide when field boundaries
	// shift: "ab"+"c" == "a"+"bc". The implementation uses a NUL separator
	// between fields to eliminate that class of collision. Verify.
	a := DeterministicXNSKey("ab", "c", "", "", "", "", 0, XNSHopDispatch)
	b := DeterministicXNSKey("a", "bc", "", "", "", "", 0, XNSHopDispatch)
	assert.NotEqual(t, a, b, "field boundaries must be significant")
}
