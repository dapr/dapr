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

package pool

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTrackIncapable covers the capability gate's accounting: the count of
// connected sidecars unable to take scheduler placement, with
// OnSchedulerPlacementCapabilityChange fired only on
// transitions between zero and non-zero.
func TestTrackIncapable(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	p := New(Options{
		OnSchedulerPlacementCapabilityChange: func() { calls.Add(1) },
	})

	ctx1, cancel1 := context.WithCancel(t.Context())
	ctx2, cancel2 := context.WithCancel(t.Context())

	assert.False(t, p.HasSchedulerPlacementIncapableSidecars())

	// First incapable sidecar: 0 -> 1 fires OnSchedulerPlacementCapabilityChange.
	p.trackCapability(ctx1, false)
	assert.True(t, p.HasSchedulerPlacementIncapableSidecars())
	assert.Equal(t, int64(1), calls.Load())

	// Second: 1 -> 2 does not.
	p.trackCapability(ctx2, false)
	assert.True(t, p.HasSchedulerPlacementIncapableSidecars())
	assert.Equal(t, int64(1), calls.Load())

	// One disconnects: 2 -> 1 does not. AfterFunc fires asynchronously.
	cancel1()
	assert.Eventually(t, func() bool {
		return p.HasSchedulerPlacementIncapableSidecars() && calls.Load() == 1
	}, time.Second*5, time.Millisecond)

	// Last one disconnects: 1 -> 0 fires OnSchedulerPlacementCapabilityChange, and the gate lifts.
	cancel2()
	assert.Eventually(t, func() bool {
		return !p.HasSchedulerPlacementIncapableSidecars() && calls.Load() == 2
	}, time.Second*5, time.Millisecond)
}

// TestTrackCapable covers the capable-sidecar count, whose transitions also
// fire OnSchedulerPlacementCapabilityChange so the gate latch is evaluated promptly when the first capable sidecar
// connects.
func TestTrackCapable(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	p := New(Options{
		OnSchedulerPlacementCapabilityChange: func() { calls.Add(1) },
	})

	ctx, cancel := context.WithCancel(t.Context())

	assert.False(t, p.HasSchedulerPlacementCapableSidecars())

	// First capable sidecar: 0 -> 1 fires OnSchedulerPlacementCapabilityChange, and does not count as incapable.
	p.trackCapability(ctx, true)
	assert.True(t, p.HasSchedulerPlacementCapableSidecars())
	assert.False(t, p.HasSchedulerPlacementIncapableSidecars())
	assert.Equal(t, int64(1), calls.Load())

	cancel()
	assert.Eventually(t, func() bool {
		return !p.HasSchedulerPlacementCapableSidecars() && calls.Load() == 2
	}, time.Second*5, time.Millisecond)
}

// TestTrackIncapableNilCallback asserts a pool without OnSchedulerPlacementCapabilityChange still
// counts, since HasSchedulerPlacementIncapableSidecars is read at stamp time.
func TestTrackIncapableNilCallback(t *testing.T) {
	t.Parallel()

	p := New(Options{})
	ctx, cancel := context.WithCancel(t.Context())

	p.trackCapability(ctx, false)
	assert.True(t, p.HasSchedulerPlacementIncapableSidecars())

	cancel()
	assert.Eventually(t, func() bool {
		return !p.HasSchedulerPlacementIncapableSidecars()
	}, time.Second*5, time.Millisecond)
}

// TestTrackAddresses covers the reported placement addresses: validated and
// bounded per report, deduplicated across sidecars, and reported only while
// a sidecar reporting them is connected.
func TestTrackAddresses(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	p := New(Options{
		OnPlacementAddressesChange: func() { calls.Add(1) },
	})

	ctx1, cancel1 := context.WithCancel(t.Context())
	ctx2, cancel2 := context.WithCancel(t.Context())

	// Malformed entries and duplicates are dropped, a new address fires
	// OnPlacementAddressesChange.
	p.trackAddresses(ctx1, []string{"", "no-port", "10.0.0.1:50005", "10.0.0.1:50005"})
	assert.ElementsMatch(t, []string{"10.0.0.1:50005"}, p.PlacementAddresses())
	assert.Equal(t, int64(1), calls.Load())

	// A second sidecar reporting the same address changes nothing.
	p.trackAddresses(ctx2, []string{"10.0.0.1:50005", "10.0.0.2:50005"})
	assert.ElementsMatch(t, []string{"10.0.0.1:50005", "10.0.0.2:50005"}, p.PlacementAddresses())
	assert.Equal(t, int64(2), calls.Load())

	// An oversized report is dropped whole, as it is client supplied.
	big := make([]string, 0, maxAddressesPerReport+1)
	for i := range maxAddressesPerReport + 1 {
		big = append(big, fmt.Sprintf("10.1.0.%d:50005", i))
	}
	p.trackAddresses(t.Context(), big)
	assert.Len(t, p.PlacementAddresses(), 2)
	assert.Equal(t, int64(2), calls.Load())

	// The shared address outlives the first sidecar, the other one leaves
	// with it. AfterFunc fires asynchronously.
	cancel2()
	assert.Eventually(t, func() bool {
		addrs := p.PlacementAddresses()
		return len(addrs) == 1 && addrs[0] == "10.0.0.1:50005" && calls.Load() == 3
	}, time.Second*5, time.Millisecond)

	cancel1()
	assert.Eventually(t, func() bool {
		return len(p.PlacementAddresses()) == 0 && calls.Load() == 4
	}, time.Second*5, time.Millisecond)
}
