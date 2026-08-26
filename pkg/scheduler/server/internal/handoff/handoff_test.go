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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlacementStreams(t *testing.T) {
	t.Parallel()

	h := New(Options{})

	assert.False(t, h.PlacementPresent())
	assert.False(t, h.PlacementStoodDown())

	// A serving stream is presence, not stood down.
	id1 := h.AddPlacementStream(false)
	assert.True(t, h.PlacementPresent())
	assert.False(t, h.PlacementStoodDown())

	// Stood down only once no stream reports serving.
	id2 := h.AddPlacementStream(true)
	assert.True(t, h.PlacementPresent())
	assert.False(t, h.PlacementStoodDown())

	h.SetPlacementStreamState(id1, true)
	assert.True(t, h.PlacementPresent())
	assert.True(t, h.PlacementStoodDown())

	// A stream reporting serving again revokes the stand-down.
	h.SetPlacementStreamState(id2, false)
	assert.False(t, h.PlacementStoodDown())
	h.SetPlacementStreamState(id2, true)
	assert.True(t, h.PlacementStoodDown())

	// A placement service which disappears takes its presence with it.
	h.RemovePlacementStream(id1)
	h.RemovePlacementStream(id2)
	assert.False(t, h.PlacementPresent())
	assert.False(t, h.PlacementStoodDown())
}

func TestServingStreamResetsAdvertised(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	h.LatchAdvertised()
	require.True(t, h.Advertised())

	// A serving placement service means the next cutover runs the
	// handshake again.
	id := h.AddPlacementStream(false)
	assert.False(t, h.Advertised())

	h.LatchAdvertised()
	h.SetPlacementStreamState(id, true)
	assert.True(t, h.Advertised(), "a stood-down report keeps the latch")

	h.SetPlacementStreamState(id, false)
	assert.False(t, h.Advertised())
}

func TestDetectionSighting(t *testing.T) {
	t.Parallel()

	h := New(Options{PlacementDNSName: "dapr-placement-server"})
	resolved := false
	h.lookupHost = func(context.Context, string) ([]string, error) {
		if resolved {
			return []string{"10.0.0.1"}, nil
		}
		return nil, assert.AnError
	}

	h.refreshDetection(t.Context())
	assert.False(t, h.PlacementPresent())

	// A placement service too old to report itself must still withhold the
	// advertisement, and cannot have stood down.
	resolved = true
	h.refreshDetection(t.Context())
	assert.True(t, h.PlacementPresent())
	assert.False(t, h.PlacementStoodDown())

	// The stream is first hand state and overrides the sighting: a
	// stood-down placement service still accepts the probe's connections.
	id := h.AddPlacementStream(true)
	assert.True(t, h.PlacementStoodDown())

	resolved = false
	h.refreshDetection(t.Context())
	assert.True(t, h.PlacementPresent(), "the stream remains")
	assert.True(t, h.PlacementStoodDown())
	h.RemovePlacementStream(id)
	assert.False(t, h.PlacementPresent())
}

func TestLocalCapabilities(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	assert.False(t, h.AnySchedulerPlacementIncapableSidecars())
	assert.False(t, h.AnySchedulerPlacementCapableSidecars())

	h.SetLocalCapabilities(true, true)
	assert.True(t, h.AnySchedulerPlacementIncapableSidecars())
	assert.True(t, h.AnySchedulerPlacementCapableSidecars())

	h.SetLocalCapabilities(false, true)
	assert.False(t, h.AnySchedulerPlacementIncapableSidecars())
	assert.True(t, h.AnySchedulerPlacementCapableSidecars())
}

func TestReady(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	assert.False(t, h.Ready(), "not ready before the first detection")

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(ctx) }()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, h.Ready())
	}, 5e9, 1e7)

	cancel()
	require.Error(t, <-errCh)
}

func TestOnChange(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	fired := 0
	h.SetOnChange(func() { fired++ })

	id := h.AddPlacementStream(false)
	h.SetPlacementStreamState(id, true)
	h.RemovePlacementStream(id)
	h.SetLocalCapabilities(true, false)
	assert.Equal(t, 4, fired)
}
