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

package disseminator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

func v2Tables(entries map[string][]string) *schedulerv1pb.PlacementTables {
	t := &schedulerv1pb.PlacementTables{
		HashAlgorithm: schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_RENDEZVOUS,
		Entries:       make(map[string]*schedulerv1pb.PlacementTable),
	}
	for actorType, addrs := range entries {
		hosts := make(map[string]*schedulerv1pb.PlacementHost, len(addrs))
		for _, addr := range addrs {
			hosts[addr] = &schedulerv1pb.PlacementHost{Address: addr, AppId: "app"}
		}
		t.Entries[actorType] = &schedulerv1pb.PlacementTable{Hosts: hosts}
	}
	return t
}

func newTestDisseminatorV2(t *testing.T) *disseminator {
	t.Helper()

	diss, _, _ := newTestDisseminator(t)
	diss.v2 = true
	diss.v2Rounds = make(map[uint64]*v2Round)

	return diss
}

func collectAcks(diss *disseminator) *[]*loops.Ack {
	acks := new([]*loops.Ack)
	diss.streamLoop = loopfake.New[loops.EventStream]().
		WithEnqueue(func(e loops.EventStream) {
			if send, ok := e.(*loops.StreamSend); ok && send.Ack != nil {
				*acks = append(*acks, send.Ack)
			}
		})
	return acks
}

func TestHandleOrderV2_SnapshotAndReadiness(t *testing.T) {
	diss := newTestDisseminatorV2(t)
	acks := collectAcks(diss)

	// Startup snapshot: empty scope, all types, followed by readiness.
	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderLock, Version: 1, Partial: true},
	}))
	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{
			Op:       loops.OrderUpdate,
			Version:  1,
			Partial:  true,
			Versions: map[string]uint64{"t1": 1},
			V2Tables: v2Tables(map[string][]string{"t1": {"a:1"}}),
		},
	}))
	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderUnlock, Version: 1, Partial: true},
	}))

	require.Len(t, *acks, 3)
	assert.Equal(t, loops.OrderLock, (*acks)[0].Op)
	assert.Equal(t, uint64(1), (*acks)[0].Version)
	assert.Equal(t, loops.OrderUpdate, (*acks)[1].Op)
	assert.Equal(t, loops.OrderUnlock, (*acks)[2].Op)

	// The fake actor table hosts no types, so the sidecar is ready after the
	// first completed round.
	assert.True(t, diss.ready.Load())
	assert.Empty(t, diss.v2Rounds)
}

func TestHandleOrderV2_ConcurrentDisjointRounds(t *testing.T) {
	diss := newTestDisseminatorV2(t)
	collectAcks(diss)

	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderLock, Version: 7, Scope: []string{"t1"}, Partial: true},
	}))
	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderLock, Version: 8, Scope: []string{"t2"}, Partial: true},
	}))

	assert.Len(t, diss.v2Rounds, 2)
	assert.True(t, diss.inflight.IsBlocked("t1"))
	assert.True(t, diss.inflight.IsBlocked("t2"))

	// Completing round 8 releases t2 only.
	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{
			Op:       loops.OrderUpdate,
			Version:  8,
			Partial:  true,
			Versions: map[string]uint64{"t2": 1},
			V2Tables: v2Tables(map[string][]string{"t2": {"b:1"}}),
		},
	}))
	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderUnlock, Version: 8, Scope: []string{"t2"}, Partial: true},
	}))

	assert.False(t, diss.inflight.IsBlocked("t2"))
	assert.True(t, diss.inflight.IsBlocked("t1"))
	assert.Len(t, diss.v2Rounds, 1)
}

func TestHandleOrderV2_UnknownSeqClosesStream(t *testing.T) {
	diss := newTestDisseminatorV2(t)

	var streamClosed bool
	diss.streamLoop = loopfake.New[loops.EventStream]().
		WithClose(func(loops.EventStream) { streamClosed = true })

	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderUpdate, Version: 99, Partial: true},
	}))
	assert.True(t, streamClosed)
}

func TestHandleOrderV2_MergeErrorClosesStream(t *testing.T) {
	diss := newTestDisseminatorV2(t)

	var streamClosed bool
	diss.streamLoop = loopfake.New[loops.EventStream]().
		WithClose(func(loops.EventStream) { streamClosed = true })

	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderLock, Version: 1, Partial: true},
	}))

	bad := v2Tables(map[string][]string{"t1": {"a:1"}})
	bad.HashAlgorithm = schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_UNKNOWN
	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderUpdate, Version: 1, Partial: true, V2Tables: bad},
	}))
	assert.True(t, streamClosed)
}

func TestHandleTimeoutV2(t *testing.T) {
	diss := newTestDisseminatorV2(t)
	collectAcks(diss)

	require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
		Order: &loops.Order{Op: loops.OrderLock, Version: 3, Scope: []string{"t1"}, Partial: true},
	}))

	var streamClosed bool
	diss.streamLoop = loopfake.New[loops.EventStream]().
		WithClose(func(loops.EventStream) { streamClosed = true })

	// A timeout for a completed/unknown round is ignored.
	diss.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 99})
	assert.False(t, streamClosed)

	// A timeout for an in-flight round closes the stream.
	diss.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 3})
	assert.True(t, streamClosed)
}
