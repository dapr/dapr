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

package handoff

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGateEncoding(t *testing.T) {
	t.Parallel()

	for _, g := range []gateEntry{
		{},
		{incapable: true},
		{capable: true},
		{incapable: true, capable: true},
	} {
		assert.Equal(t, g, decodeGate(encodeGate(g)))
	}
}

func TestApplyKV(t *testing.T) {
	t.Parallel()

	h := New(Options{ID: "s1"})

	h.applyKV(keyPresent, "true", false)
	h.applyKV(gatePrefix+"s1", "incapable", false)
	h.applyKV(gatePrefix+"s2", "capable", false)
	assert.True(t, h.PlacementPresent())
	assert.False(t, h.PlacementStoodDown())
	assert.False(t, h.Advertised())
	assert.True(t, h.AnySchedulerPlacementIncapableSidecars())
	assert.True(t, h.AnySchedulerPlacementCapableSidecars())

	h.applyKV(gatePrefix+"s1", "", true)
	assert.False(t, h.AnySchedulerPlacementIncapableSidecars(), "an expired gate entry must stop withholding")
	assert.True(t, h.AnySchedulerPlacementCapableSidecars())

	h.applyKV(keyStoodDown, "true", false)
	h.applyKV(keyAdvertised, "true", false)
	assert.True(t, h.PlacementStoodDown())
	assert.True(t, h.Advertised())
}

func TestDNSSightings(t *testing.T) {
	t.Parallel()

	t.Run("any scheduler sighting the svc counts as present", func(t *testing.T) {
		t.Parallel()
		h := New(Options{ID: "s1", PlacementDNSName: "dapr-placement-server"})

		h.applyKV(sightingPrefix+"s2", "dns", false)
		assert.True(t, h.PlacementPresent(),
			"a placement service too old to announce itself must still withhold the advertisement")

		h.applyKV(sightingPrefix+"s2", "", true)
		assert.False(t, h.PlacementPresent())
	})

	t.Run("announcement and sightings are independent presence signals", func(t *testing.T) {
		t.Parallel()
		h := New(Options{ID: "s1", PlacementDNSName: "dapr-placement-server"})

		h.applyKV(keyPresent, "true", false)
		assert.True(t, h.PlacementPresent())

		h.applyKV(sightingPrefix+"s1", "probe", false)
		h.applyKV(keyPresent, "", true)
		assert.True(t, h.PlacementPresent(),
			"a still-deployed placement service must keep withholding after a stale announcement clears")

		h.applyKV(sightingPrefix+"s1", "", true)
		assert.False(t, h.PlacementPresent())
	})
}
