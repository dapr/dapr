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

package cron

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/kit/events/broadcaster"
)

func TestPlacementLeader(t *testing.T) {
	t.Parallel()

	host := func(addr string, placement bool) *schedulerv1pb.Host {
		return &schedulerv1pb.Host{Address: addr, SchedulerPlacementEnabled: placement}
	}

	tests := map[string]struct {
		hosts []*schedulerv1pb.Host
		exp   string
	}{
		"no hosts": {
			hosts: nil,
			exp:   "",
		},
		"no capable hosts": {
			hosts: []*schedulerv1pb.Host{host("a:1", false), host("b:1", false)},
			exp:   "",
		},
		"first sorted capable host wins": {
			hosts: []*schedulerv1pb.Host{host("c:1", true), host("a:1", true), host("b:1", true)},
			exp:   "a:1",
		},
		"incapable hosts are skipped even when sorted first": {
			hosts: []*schedulerv1pb.Host{host("a:1", false), host("c:1", true), host("b:1", true)},
			exp:   "b:1",
		},
		"single capable host": {
			hosts: []*schedulerv1pb.Host{host("z:1", true)},
			exp:   "z:1",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.exp, placementLeader(test.hosts))
		})
	}
}

type fakePool struct {
	incapable bool
	capable   bool

	count int32
	idx   int32
}

func (f *fakePool) SetSchedulerInfo(count, idx int32) { f.count, f.idx = count, idx }
func (f *fakePool) HasSchedulerPlacementIncapableSidecars() bool {
	return f.incapable
}

func (f *fakePool) HasSchedulerPlacementCapableSidecars() bool {
	return f.capable
}

type fakePlacementLeader struct {
	leader     *bool
	hasStreams bool
}

func (f *fakePlacementLeader) SetLeader(leader bool) { f.leader = &leader }
func (f *fakePlacementLeader) HasPlacementStreams() bool {
	return f.hasStreams
}

func TestLeadershipHandle(t *testing.T) {
	t.Parallel()

	anyHost := func(t *testing.T, addr string, placement bool) *anypb.Any {
		t.Helper()
		a, err := anypb.New(&schedulerv1pb.Host{Address: addr, SchedulerPlacementEnabled: placement})
		require.NoError(t, err)
		return a
	}

	newLeadership := func(pool *fakePool, place *fakePlacementLeader) (*leadership, chan []*schedulerv1pb.Host) {
		var lock sync.RWMutex
		var broadcastHosts []*schedulerv1pb.Host
		bc := broadcaster.New[[]*schedulerv1pb.Host]()
		ch := make(chan []*schedulerv1pb.Host, 4)
		bc.Subscribe(t.Context(), ch)
		return &leadership{
			hostBroadcaster: bc,
			lock:            &lock,
			broadcastHosts:  &broadcastHosts,
			readyCh:         make(chan struct{}),
			ownAddress:      "a:1",
			pool:            pool,
			placement:       place,
		}, ch
	}

	t.Run("no incapable sidecars advertises the leader and capability", func(t *testing.T) {
		t.Parallel()
		pool := &fakePool{capable: true}
		place := new(fakePlacementLeader)
		l, ch := newLeadership(pool, place)

		require.NoError(t, l.Handle(t.Context(),
			[]*anypb.Any{anyHost(t, "a:1", true), anyHost(t, "b:1", true)}))

		hosts := <-ch
		require.Len(t, hosts, 2)
		assert.True(t, hosts[0].GetLeader())
		assert.True(t, hosts[0].GetSchedulerPlacementEnabled())
		assert.False(t, hosts[1].GetLeader())
		assert.True(t, hosts[1].GetSchedulerPlacementEnabled())
		require.NotNil(t, place.leader)
		assert.True(t, *place.leader)
	})

	t.Run("incapable sidecar withholds the leader and masks capability", func(t *testing.T) {
		t.Parallel()
		pool := &fakePool{incapable: true}
		place := new(fakePlacementLeader)
		l, ch := newLeadership(pool, place)

		require.NoError(t, l.Handle(t.Context(),
			[]*anypb.Any{anyHost(t, "a:1", true), anyHost(t, "b:1", true)}))

		hosts := <-ch
		require.Len(t, hosts, 2)
		for _, host := range hosts {
			// Sidecars must see a cluster which does not serve placement, so
			// they use the standalone placement service: one authority.
			assert.False(t, host.GetLeader())
			assert.False(t, host.GetSchedulerPlacementEnabled())
		}
		require.NotNil(t, place.leader)
		assert.False(t, *place.leader)
	})

	t.Run("nil event re-broadcasts the last table under the new capability state", func(t *testing.T) {
		t.Parallel()
		pool := &fakePool{incapable: true, capable: true}
		place := new(fakePlacementLeader)
		l, ch := newLeadership(pool, place)

		require.NoError(t, l.Handle(t.Context(),
			[]*anypb.Any{anyHost(t, "a:1", true)}))
		hosts := <-ch
		assert.False(t, hosts[0].GetLeader())

		// The last incapable sidecar disconnects: the pool signals with a
		// nil event and the same table is re-broadcast, now advertising
		// placement.
		pool.incapable = false
		require.NoError(t, l.Handle(t.Context(), nil))

		hosts = <-ch
		require.Len(t, hosts, 1)
		assert.True(t, hosts[0].GetLeader())
		assert.True(t, hosts[0].GetSchedulerPlacementEnabled())
		require.NotNil(t, place.leader)
		assert.True(t, *place.leader)
	})

	t.Run("nil event before any table is a no-op", func(t *testing.T) {
		t.Parallel()
		pool := new(fakePool)
		place := new(fakePlacementLeader)
		l, ch := newLeadership(pool, place)

		require.NoError(t, l.Handle(t.Context(), nil))
		select {
		case hosts := <-ch:
			t.Fatalf("unexpected broadcast: %v", hosts)
		default:
		}
		assert.Nil(t, place.leader)
	})
}

// TestLeadershipAdvertisementPermanence asserts the advertisement cannot be
// withheld again once placement has been advertised to a capable sidecar: an
// old sidecar joining a settled cluster must not revoke it and drop every
// placement stream.
func TestLeadershipAdvertisementPermanence(t *testing.T) {
	t.Parallel()

	anyHost := func(t *testing.T, addr string, placement bool) *anypb.Any {
		t.Helper()
		a, err := anypb.New(&schedulerv1pb.Host{Address: addr, SchedulerPlacementEnabled: placement})
		require.NoError(t, err)
		return a
	}

	var lock sync.RWMutex
	var broadcastHosts []*schedulerv1pb.Host
	bc := broadcaster.New[[]*schedulerv1pb.Host]()
	ch := make(chan []*schedulerv1pb.Host, 4)
	bc.Subscribe(t.Context(), ch)
	pool := new(fakePool)
	place := new(fakePlacementLeader)
	l := &leadership{
		hostBroadcaster: bc,
		lock:            &lock,
		broadcastHosts:  &broadcastHosts,
		readyCh:         make(chan struct{}),
		ownAddress:      "a:1",
		pool:            pool,
		placement:       place,
	}

	// An empty cluster advertises no leader, but keeps the capability bit:
	// a booting sidecar reads capable-but-leaderless and waits, and a
	// placement service must not mistake the boot broadcast for a cutover.
	require.NoError(t, l.Handle(t.Context(), []*anypb.Any{anyHost(t, "a:1", true)}))
	hosts := <-ch
	require.False(t, hosts[0].GetLeader())
	require.True(t, hosts[0].GetSchedulerPlacementEnabled())
	require.False(t, l.advertised)

	// An old sidecar connects first: the gate masks the capability too.
	pool.incapable = true
	require.NoError(t, l.Handle(t.Context(), nil))
	hosts = <-ch
	require.False(t, hosts[0].GetLeader())
	require.False(t, hosts[0].GetSchedulerPlacementEnabled())

	// The old sidecar leaves, a capable sidecar is connected, and one has
	// taken a placement stream: the advertisement resumes and, with real
	// evidence of cutover, becomes permanent.
	pool.incapable = false
	pool.capable = true
	place.hasStreams = true
	require.NoError(t, l.Handle(t.Context(), nil))
	hosts = <-ch
	require.True(t, hosts[0].GetLeader())
	require.True(t, l.advertised)

	// A late old sidecar connects: placement stays advertised and the
	// unchanged table is not re-broadcast.
	pool.incapable = true
	require.NoError(t, l.Handle(t.Context(), nil))
	select {
	case hosts = <-ch:
		t.Fatalf("an unchanged table must not be re-broadcast: %v", hosts)
	default:
	}
	require.NotNil(t, place.leader)
	assert.True(t, *place.leader)
}

// TestLeadershipAdvertisementSurvivesCapableDip asserts an advertised
// leader is not withdrawn when the capable sidecar count transiently drops
// to zero. Sidecars re-establish their jobs streams when their target types
// change, and a withdrawn leader would make every sidecar drop its
// placement stream and halt its actors.
func TestLeadershipAdvertisementSurvivesCapableDip(t *testing.T) {
	t.Parallel()

	anyHost := func(t *testing.T, addr string, placement bool) *anypb.Any {
		t.Helper()
		a, err := anypb.New(&schedulerv1pb.Host{Address: addr, SchedulerPlacementEnabled: placement})
		require.NoError(t, err)
		return a
	}

	var lock sync.RWMutex
	var broadcastHosts []*schedulerv1pb.Host
	bc := broadcaster.New[[]*schedulerv1pb.Host]()
	ch := make(chan []*schedulerv1pb.Host, 4)
	bc.Subscribe(t.Context(), ch)
	pool := new(fakePool)
	place := new(fakePlacementLeader)
	l := &leadership{
		hostBroadcaster: bc,
		lock:            &lock,
		broadcastHosts:  &broadcastHosts,
		readyCh:         make(chan struct{}),
		ownAddress:      "a:1",
		pool:            pool,
		placement:       place,
	}

	// Before the first advertisement, no capable sidecar means no leader.
	require.NoError(t, l.Handle(t.Context(), []*anypb.Any{anyHost(t, "a:1", true)}))
	hosts := <-ch
	require.False(t, hosts[0].GetLeader())
	require.False(t, l.advertised)

	// A capable sidecar connects and takes a placement stream: advertised.
	pool.capable = true
	place.hasStreams = true
	require.NoError(t, l.Handle(t.Context(), nil))
	hosts = <-ch
	require.True(t, hosts[0].GetLeader())
	require.True(t, l.advertised)

	// The sidecar re-establishes its jobs streams: the capable count dips
	// to zero, and the leader must stay advertised, with the unchanged
	// table not re-broadcast.
	pool.capable = false
	require.NoError(t, l.Handle(t.Context(), nil))
	select {
	case hosts = <-ch:
		t.Fatalf("an unchanged table must not be re-broadcast: %v", hosts)
	default:
	}
	require.True(t, l.advertised)

	// The streams return: still advertised, still no re-broadcast.
	pool.capable = true
	require.NoError(t, l.Handle(t.Context(), nil))
	select {
	case hosts = <-ch:
		t.Fatalf("an unchanged table must not be re-broadcast: %v", hosts)
	default:
	}
}

// TestLeadershipMalformedTableNotReplayed asserts a table which fails to
// unmarshal is not saved: a later capability signal must be a no-op rather
// than replaying the malformed table and erroring forever.
func TestLeadershipMalformedTableNotReplayed(t *testing.T) {
	t.Parallel()

	var lock sync.RWMutex
	var broadcastHosts []*schedulerv1pb.Host
	bc := broadcaster.New[[]*schedulerv1pb.Host]()
	l := &leadership{
		hostBroadcaster: bc,
		lock:            &lock,
		broadcastHosts:  &broadcastHosts,
		readyCh:         make(chan struct{}),
		ownAddress:      "a:1",
		pool:            new(fakePool),
		placement:       new(fakePlacementLeader),
	}

	// A payload of the wrong type fails UnmarshalTo.
	bad, err := anypb.New(&schedulerv1pb.Job{})
	require.NoError(t, err)
	require.Error(t, l.Handle(t.Context(), []*anypb.Any{bad}))

	// The malformed table was not saved: the capability signal is a no-op.
	require.Nil(t, l.lastCronTable)
	require.NoError(t, l.Handle(t.Context(), nil))
}

type fakeHandoff struct {
	present    bool
	stoodDown  bool
	advertised bool
	incapable  bool
	capable    bool

	latched int
}

func (f *fakeHandoff) PlacementPresent() bool                       { return f.present }
func (f *fakeHandoff) PlacementStoodDown() bool                     { return f.stoodDown }
func (f *fakeHandoff) Advertised() bool                             { return f.advertised }
func (f *fakeHandoff) AnySchedulerPlacementIncapableSidecars() bool { return f.incapable }
func (f *fakeHandoff) AnySchedulerPlacementCapableSidecars() bool   { return f.capable }
func (f *fakeHandoff) LatchAdvertised()                             { f.latched++ }

func TestLeadershipStandDownHandshake(t *testing.T) {
	t.Parallel()

	anyHost := func(t *testing.T, addr string, placement bool) *anypb.Any {
		t.Helper()
		a, err := anypb.New(&schedulerv1pb.Host{Address: addr, SchedulerPlacementEnabled: placement})
		require.NoError(t, err)
		return a
	}

	newLeadership := func(hoff *fakeHandoff) (*leadership, chan []*schedulerv1pb.Host) {
		var lock sync.RWMutex
		var broadcastHosts []*schedulerv1pb.Host
		bc := broadcaster.New[[]*schedulerv1pb.Host]()
		ch := make(chan []*schedulerv1pb.Host, 4)
		bc.Subscribe(t.Context(), ch)
		return &leadership{
			hostBroadcaster: bc,
			lock:            &lock,
			broadcastHosts:  &broadcastHosts,
			readyCh:         make(chan struct{}),
			ownAddress:      "a:1",
			pool:            new(fakePool),
			placement:       &fakePlacementLeader{hasStreams: true},
			handoff:         hoff,
		}, ch
	}

	t.Run("announced placement withholds the leader and signals cutover pending", func(t *testing.T) {
		t.Parallel()
		hoff := &fakeHandoff{present: true, capable: true}
		l, ch := newLeadership(hoff)
		table := []*anypb.Any{anyHost(t, "a:1", true), anyHost(t, "b:1", true)}

		require.NoError(t, l.Handle(t.Context(), table))

		hosts := <-ch
		require.Len(t, hosts, 2)
		assert.False(t, hosts[0].GetLeader())
		assert.False(t, hosts[0].GetSchedulerPlacementEnabled())
		assert.True(t, hosts[0].GetPlacementCutoverPending(),
			"the elected leader must signal the placement service to stand down")
		assert.False(t, hosts[1].GetPlacementCutoverPending())
		assert.Zero(t, hoff.latched, "a withheld advertisement must not latch")
	})

	t.Run("stood down placement lifts the withhold and latches", func(t *testing.T) {
		t.Parallel()
		hoff := &fakeHandoff{present: true, stoodDown: true, capable: true}
		l, ch := newLeadership(hoff)
		table := []*anypb.Any{anyHost(t, "a:1", true), anyHost(t, "b:1", true)}

		require.NoError(t, l.Handle(t.Context(), table))

		hosts := <-ch
		require.Len(t, hosts, 2)
		assert.True(t, hosts[0].GetLeader())
		assert.True(t, hosts[0].GetSchedulerPlacementEnabled())
		assert.False(t, hosts[0].GetPlacementCutoverPending())
		assert.Equal(t, 1, hoff.latched)
	})

	t.Run("incapable sidecar anywhere in the cluster withholds before cutover pending", func(t *testing.T) {
		t.Parallel()
		hoff := &fakeHandoff{present: true, incapable: true, capable: true}
		l, ch := newLeadership(hoff)
		table := []*anypb.Any{anyHost(t, "a:1", true)}

		require.NoError(t, l.Handle(t.Context(), table))

		hosts := <-ch
		require.Len(t, hosts, 1)
		assert.False(t, hosts[0].GetLeader())
		assert.False(t, hosts[0].GetPlacementCutoverPending(),
			"the placement service must not drain while old sidecars still need it")
	})

	t.Run("replicated advertised latch survives incapable sidecars", func(t *testing.T) {
		t.Parallel()
		hoff := &fakeHandoff{advertised: true, incapable: true, capable: true}
		l, ch := newLeadership(hoff)
		table := []*anypb.Any{anyHost(t, "a:1", true)}

		require.NoError(t, l.Handle(t.Context(), table))

		hosts := <-ch
		require.Len(t, hosts, 1)
		assert.True(t, hosts[0].GetLeader())
		assert.True(t, hosts[0].GetSchedulerPlacementEnabled())
	})

	t.Run("no placement announced advertises without a handshake", func(t *testing.T) {
		t.Parallel()
		hoff := &fakeHandoff{capable: true}
		l, ch := newLeadership(hoff)
		table := []*anypb.Any{anyHost(t, "a:1", true)}

		require.NoError(t, l.Handle(t.Context(), table))

		hosts := <-ch
		require.Len(t, hosts, 1)
		assert.True(t, hosts[0].GetLeader())
		assert.False(t, hosts[0].GetPlacementCutoverPending())
	})
}
