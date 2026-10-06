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

package watchhosts

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/healthz"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/clients"
	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops"
	"github.com/dapr/dapr/pkg/runtime/scheduler/leadership"
	"github.com/dapr/dapr/pkg/security/fake"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

// TestHandleHostsReloadDedup asserts client connections are only reloaded
// when the address list changes: reloading on identical broadcasts cycles
// every scheduler stream, and that churn re-broadcasts back into this loop.
func TestHandleHostsReloadDedup(t *testing.T) {
	t.Parallel()

	var reloads atomic.Int64
	hostLoop := loopfake.New[loops.EventHost]().WithEnqueue(func(e loops.EventHost) {
		if _, ok := e.(*loops.ReloadClients); ok {
			reloads.Add(1)
		}
	})

	w := New(Options{
		Addresses: []string{"127.0.0.1:1"},
		Healthz:   healthz.New(),
		Security:  fake.New(),
		Clients:   clients.New(clients.Options{Security: fake.New()}),
		HostLoop:  hostLoop,
	})

	resp := func(addrs ...string) *schedulerv1pb.WatchHostsResponse {
		hosts := make([]*schedulerv1pb.Host, len(addrs))
		for i, a := range addrs {
			hosts[i] = &schedulerv1pb.Host{Address: a}
		}
		return &schedulerv1pb.WatchHostsResponse{Hosts: hosts}
	}

	require.NoError(t, w.handleHosts(t.Context(), resp("127.0.0.1:1")))
	assert.Equal(t, int64(1), reloads.Load())

	require.NoError(t, w.handleHosts(t.Context(), resp("127.0.0.1:1")))
	assert.Equal(t, int64(1), reloads.Load(), "an identical list must not reload")

	require.NoError(t, w.handleHosts(t.Context(), resp("127.0.0.1:1", "127.0.0.1:2")))
	assert.Equal(t, int64(2), reloads.Load(), "a changed list must reload")
}

// TestHandleHostsLeadership asserts only a broadcast with no capable host
// reports unsupported. Capable without a leader means wait.
func TestHandleHostsLeadership(t *testing.T) {
	t.Parallel()

	host := func(addr string, capable, leader bool) *schedulerv1pb.Host {
		return &schedulerv1pb.Host{Address: addr, SchedulerPlacementEnabled: capable, Leader: leader}
	}

	newWatchHosts := func() (*WatchHosts, *leadership.Leadership) {
		ldr := leadership.New()
		return New(Options{
			Addresses:  []string{"127.0.0.1:1"},
			Healthz:    healthz.New(),
			Security:   fake.New(),
			Clients:    clients.New(clients.Options{Security: fake.New()}),
			HostLoop:   loopfake.New[loops.EventHost](),
			Leadership: ldr,
		}), ldr
	}

	t.Run("capable with a leader sets the leader", func(t *testing.T) {
		t.Parallel()
		w, ldr := newWatchHosts()
		require.NoError(t, w.handleHosts(t.Context(), &schedulerv1pb.WatchHostsResponse{Hosts: []*schedulerv1pb.Host{
			host("127.0.0.1:1", true, true), host("127.0.0.1:2", true, false),
		}}))
		addr, unsupported, _ := ldr.Leader()
		assert.Equal(t, "127.0.0.1:1", addr)
		assert.False(t, unsupported)
	})

	t.Run("capable but leaderless waits", func(t *testing.T) {
		t.Parallel()
		w, ldr := newWatchHosts()
		require.NoError(t, w.handleHosts(t.Context(), &schedulerv1pb.WatchHostsResponse{Hosts: []*schedulerv1pb.Host{
			host("127.0.0.1:1", true, false), host("127.0.0.1:2", true, false),
		}}))
		addr, unsupported, _ := ldr.Leader()
		assert.Empty(t, addr)
		assert.False(t, unsupported, "a leaderless broadcast must not report the cluster as not serving placement")
		assert.True(t, ldr.Reachable())
	})

	t.Run("one capable host is enough to wait", func(t *testing.T) {
		t.Parallel()
		w, ldr := newWatchHosts()
		require.NoError(t, w.handleHosts(t.Context(), &schedulerv1pb.WatchHostsResponse{Hosts: []*schedulerv1pb.Host{
			host("127.0.0.1:1", false, false), host("127.0.0.1:2", true, false),
		}}))
		_, unsupported, _ := ldr.Leader()
		assert.False(t, unsupported)
	})

	t.Run("no capable host reports unsupported", func(t *testing.T) {
		t.Parallel()
		w, ldr := newWatchHosts()
		require.NoError(t, w.handleHosts(t.Context(), &schedulerv1pb.WatchHostsResponse{Hosts: []*schedulerv1pb.Host{
			host("127.0.0.1:1", false, false), host("127.0.0.1:2", false, false),
		}}))
		addr, unsupported, _ := ldr.Leader()
		assert.Empty(t, addr)
		assert.True(t, unsupported)
	})

	t.Run("a leaderless broadcast after a leader keeps the sidecar waiting, not defecting", func(t *testing.T) {
		t.Parallel()
		w, ldr := newWatchHosts()
		require.NoError(t, w.handleHosts(t.Context(), &schedulerv1pb.WatchHostsResponse{Hosts: []*schedulerv1pb.Host{
			host("127.0.0.1:1", true, true),
		}}))
		_, _, changed := ldr.Leader()

		require.NoError(t, w.handleHosts(t.Context(), &schedulerv1pb.WatchHostsResponse{Hosts: []*schedulerv1pb.Host{
			host("127.0.0.1:1", true, false), host("127.0.0.1:3", true, false),
		}}))
		select {
		case <-changed:
		default:
			require.Fail(t, "the leader change must be signalled")
		}
		addr, unsupported, _ := ldr.Leader()
		assert.Empty(t, addr)
		assert.False(t, unsupported)
	})
}
