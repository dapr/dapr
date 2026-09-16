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
	"fmt"
	"net"
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
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(rollback))
}

// rollback asserts the scheduler placement authority hands back to a
// placement service without restarting sidecars once one is deployed again.
// Its presence alone withholds the scheduler's placement leader, and the
// sidecar defects back to its configured placement service. The test pins
// convergence: each sidecar follows one authority at a time from its
// WatchHosts view, and presence acts on the next detection cycle rather
// than through a handshake, so a transition is settled by observation, not
// negotiated.
type rollback struct {
	daprds [3]*daprd.Daprd
	sched  *scheduler.Scheduler
	// place is not running at first: the sidecar's configured placement
	// address holds nothing until the rollback deploys it.
	place *placement.Placement

	invoked atomic.Int64
}

func (r *rollback) Setup(t *testing.T) []framework.Option {
	r.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	// The placement port must refuse connections until the placement service
	// runs: the framework's reservation listener would satisfy the
	// scheduler's presence probe on a placement service that does not exist.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	placePort := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())
	r.place = placement.New(t, placement.WithPort(placePort))

	procs := make([]process.Interface, 0, 1+2*len(r.daprds))
	procs = append(procs, r.sched)
	for i := range r.daprds {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r2 *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, r2 *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r2 *http.Request) {
			if r2.Method != http.MethodDelete {
				r.invoked.Add(1)
			}
		})
		srv := prochttp.New(t, prochttp.WithHandler(handler))
		r.daprds[i] = daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(srv.Port()),
			daprd.WithScheduler(r.sched),
			daprd.WithPlacementAddresses(r.place.Address()),
		)
		procs = append(procs, srv, r.daprds[i])
	}

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (r *rollback) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)
	for _, d := range r.daprds {
		d.WaitUntilRunning(t, ctx)
	}

	// No placement service is present, so the scheduler advertises the
	// placement leader.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, serr := r.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if !assert.NoError(c, serr) {
			return
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, serr := stream.Recv()
		if !assert.NoError(c, serr) {
			return
		}
		leader := false
		for _, host := range resp.GetHosts() {
			leader = leader || host.GetLeader()
		}
		assert.True(c, leader)
	}, time.Second*30, time.Millisecond*50)

	gclient := r.daprds[0].GRPCClient(t, ctx)

	const instances = 30
	ids := make([]string, instances)
	for i := range ids {
		ids[i] = fmt.Sprintf("rollback-%d", i)
	}
	invokeAll := func() {
		for _, id := range ids {
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
					ActorType: "myactortype",
					ActorId:   id,
					Method:    "foo",
				})
				assert.NoError(c, err)
			}, time.Second*20, time.Millisecond*10)
		}
	}
	invokeAll()

	meta, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	require.Equal(t, "placement: connected", meta.GetActorRuntime().GetPlacement())

	// The scheduler holds the sidecar's placement stream and the leader.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var streams float64
		var leader float64
		for k, v := range r.sched.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
			if strings.HasPrefix(k, "dapr_scheduler_placement_leader") {
				leader += v
			}
		}
		assert.GreaterOrEqual(c, streams, float64(1))
		assert.Equal(c, 1, int(leader))
	}, time.Second*10, time.Millisecond*50)

	invokedBefore := r.invoked.Load()

	// No sample across the rollback may show both authorities serving.
	pollCtx, pollCancel := context.WithCancel(ctx)
	pollDone := make(chan struct{})
	var bothServing atomic.Bool
	go func() {
		defer close(pollDone)
		q := new(quietT)
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-time.After(time.Millisecond * 100):
			}
			var runtimes, leader float64
			for k, v := range r.place.Metrics(q, pollCtx).All() {
				if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
					runtimes += v
				}
			}
			for k, v := range r.sched.Metrics(q, pollCtx).All() {
				if strings.HasPrefix(k, "dapr_scheduler_placement_leader") {
					leader += v
				}
			}
			if runtimes > 0 && leader > 0 {
				bothServing.Store(true)
			}
		}
	}()

	// Roll back: a placement service is deployed on the address the sidecar
	// was configured with. Its presence withholds the scheduler's placement
	// leader, and the sidecar adopts the placement service without a
	// restart.
	r.place.Run(t, ctx)
	t.Cleanup(func() { r.place.Cleanup(t) })
	r.place.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var runtimes float64
		for k, v := range r.place.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
				runtimes += v
			}
		}
		assert.GreaterOrEqual(c, runtimes, float64(1))

		var streams float64
		var leader float64
		for k, v := range r.sched.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
			if strings.HasPrefix(k, "dapr_scheduler_placement_leader") {
				leader += v
			}
		}
		assert.Zero(c, streams)
		assert.Zero(c, leader)
	}, time.Second*30, time.Millisecond*50)

	invokeAll()
	assert.Greater(t, r.invoked.Load(), invokedBefore)

	meta, err = gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, "placement: connected", meta.GetActorRuntime().GetPlacement())
	assert.Equal(t, rtv1.ActorRuntime_RUNNING, meta.GetActorRuntime().GetRuntimeStatus())

	// The sidecar's own accounting agrees: the actor is active on this host.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		active := 0
		for _, aa := range r.daprds[0].GetMetaActorRuntime(c, ctx).ActiveActors {
			if aa.Type == "myactortype" {
				active = aa.Count
			}
		}
		assert.Positive(c, active)
	}, time.Second*10, time.Millisecond*50)

	pollCancel()
	<-pollDone
	require.False(t, bothServing.Load(),
		"no sample across the rollback may show both authorities serving")

	// The rollback reset the advertisement latch: a second cutover with
	// only an incapable sidecar attached must wait for a capable one.
	for _, d := range r.daprds {
		d.Cleanup(t)
	}
	oldCtx, oldCancel := context.WithCancel(ctx)
	t.Cleanup(oldCancel)
	r.sched.WatchJobsSuccess(t, oldCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "old-sidecar",
		Namespace: "default",
	})
	r.place.Cleanup(t)

	leader := func() bool {
		stream, serr := r.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if serr != nil {
			return false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, serr := stream.Recv()
		if serr != nil {
			return false
		}
		for _, host := range resp.GetHosts() {
			if host.GetLeader() {
				return true
			}
		}
		return false
	}
	require.Never(t, leader, time.Second*10, time.Millisecond*250,
		"an incapable sidecar alone must not reopen the advertisement latch")

	capCtx, capCancel := context.WithCancel(ctx)
	t.Cleanup(capCancel)
	r.sched.WatchJobsSuccess(t, capCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, leader())
	}, time.Second*30, time.Millisecond*100)
}

type quietT struct{}

func (*quietT) Errorf(string, ...any) {}
