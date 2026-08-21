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

package schedulerplacement

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(gate))
}

// gate tests the capability gate: the placement advertisement is withheld
// while any connected sidecar does not report supports_scheduler_placement, so a
// cluster mid version-rollout keeps every sidecar on the placement service.
// The gate lifts by itself when the last incapable sidecar disconnects.
type gate struct {
	daprd *daprd.Daprd
	sched *scheduler.Scheduler
	place *placement.Placement

	invoked atomic.Int64
}

func (g *gate) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {
		g.invoked.Add(1)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	g.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	g.place = placement.New(t)

	g.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(g.sched),
		daprd.WithPlacementAddresses(g.place.Address()),
	)

	// daprd is run manually so the incapable sidecar's stream is established
	// first
	return []framework.Option{
		framework.WithProcesses(g.sched, g.place, srv),
	}
}

func (g *gate) Run(t *testing.T, ctx context.Context) {
	g.sched.WaitUntilRunning(t, ctx)
	g.place.WaitUntilRunning(t, ctx)

	// An "old" sidecar: a WatchJobs stream whose initial does not set
	// supports_scheduler_placement, exactly what a daprd predating scheduler
	// placement sends.
	oldCtx, oldCancel := context.WithCancel(ctx)
	t.Cleanup(oldCancel)
	g.sched.WatchJobsSuccess(t, oldCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "old-sidecar",
		Namespace: "default",
	})

	// assertAdvertised checks whether any scheduler host advertises placement
	// capability and leadership on WatchHosts.
	assertAdvertised := func(t *testing.T, exp bool) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			stream, err := g.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
			if !assert.NoError(c, err) {
				return
			}
			defer stream.CloseSend()
			resp, err := stream.Recv()
			if !assert.NoError(c, err) {
				return
			}
			capable, leader := false, false
			for _, host := range resp.GetHosts() {
				capable = capable || host.GetSchedulerPlacementEnabled()
				leader = leader || host.GetLeader()
			}
			assert.Equal(c, exp, capable)
			assert.Equal(c, exp, leader)
		}, time.Second*20, time.Millisecond*10)
	}

	// The scheduler withholds the placement advertisement while the old
	// sidecar is connected.
	assertAdvertised(t, false)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		all := g.sched.Metrics(c, ctx).All()
		assert.Equal(c, 1, int(all["dapr_scheduler_placement_incapable_sidecars"]))
	}, time.Second*20, time.Millisecond*10)

	// A new daprd starting under the gate uses the placement service, the
	// same authority as the old sidecar.
	g.daprd.Run(t, ctx)
	t.Cleanup(func() { g.daprd.Cleanup(t) })
	g.daprd.WaitUntilRunning(t, ctx)

	gclient := g.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)
	invokedUnderGate := g.invoked.Load()
	assert.Positive(t, invokedUnderGate)

	// The scheduler holds no placement stream under the gate: the actors
	// above were served by the placement service.
	var streamsUnderGate float64
	for k, v := range g.sched.Metrics(t, ctx).All() {
		if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
			streamsUnderGate += v
		}
	}
	assert.Zero(t, streamsUnderGate)

	// The gate must hold while the old sidecar is connected, regardless of
	// the capable daprd having connected since.
	assertAdvertised(t, false)

	// The old sidecar disconnects: the gate lifts, but the daprd reported
	// its placement address and the placement service still serves there, so
	// the schedulers keep withholding the advertisement.
	oldCancel()
	assert.Never(t, func() bool {
		stream, err := g.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return false
		}
		for _, host := range resp.GetHosts() {
			if host.GetLeader() {
				return true
			}
		}
		return false
	}, time.Second*3, time.Millisecond*250,
		"no placement leader may be advertised while a reported placement service still serves")

	// The placement service is torn down: with nothing serving placement the
	// advertisement appears on its own.
	g.place.Cleanup(t)
	assertAdvertised(t, true)

	// The running daprd adopts scheduler placement on its own, no restart
	// needed, and actors keep working.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*10)
	invokedAfterCutover := g.invoked.Load()
	assert.Greater(t, invokedAfterCutover, invokedUnderGate)

	// The scheduler holds the sidecar's placement stream.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var streams float64
		for k, v := range g.sched.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(c, streams, float64(1))
	}, time.Second*10, time.Millisecond*50)

	// The latch holds: a late old sidecar must not revoke the
	// advertisement and drop every placement stream.
	lateCtx, lateCancel := context.WithCancel(ctx)
	t.Cleanup(lateCancel)
	lateTriggered := g.sched.WatchJobsSuccess(t, lateCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "late-old-sidecar",
		Namespace: "default",
	})

	// A job round-trip through the late sidecar's stream proves the scheduler
	// has fully registered the connection, so the assertions below cannot
	// pass on a stale, pre-registration view of the gate.
	_, err := g.sched.Client(t, ctx).ScheduleJob(ctx, g.sched.JobNowJob("latch-probe", "default", "late-old-sidecar"))
	require.NoError(t, err)
	select {
	case name := <-lateTriggered:
		assert.Equal(t, "latch-probe", name)
	case <-time.After(time.Second * 20):
		require.Fail(t, "late old sidecar's WatchJobs stream never received the probe job")
	}

	// Placement stays advertised and actors keep working.
	assertAdvertised(t, true)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ierr := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, ierr)
	}, time.Second*10, time.Millisecond*10)
	assert.Greater(t, g.invoked.Load(), invokedAfterCutover)
}
