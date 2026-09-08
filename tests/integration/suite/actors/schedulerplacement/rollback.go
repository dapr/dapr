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
// sidecar defects back to its configured placement service, so a single
// authority holds throughout.
type rollback struct {
	daprd *daprd.Daprd
	sched *scheduler.Scheduler
	// place is not running at first: the sidecar's configured placement
	// address holds nothing until the rollback deploys it.
	place *placement.Placement

	invoked atomic.Int64
}

func (r *rollback) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r2 *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r2 *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r2 *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r2 *http.Request) {
		r.invoked.Add(1)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))

	r.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	// The placement port must refuse connections until the placement service
	// runs: the framework's reservation listener would satisfy the
	// scheduler's presence probe on a placement service that does not exist
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	placePort := lis.Addr().(*net.TCPAddr).Port
	require.NoError(t, lis.Close())
	r.place = placement.New(t, placement.WithPort(placePort))

	r.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(r.sched),
		daprd.WithPlacementAddresses(r.place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(r.sched, srv, r.daprd),
	}
}

func (r *rollback) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

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

	gclient := r.daprd.GRPCClient(t, ctx)

	// Actors are placed by the scheduler.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)

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

	// Actors keep working through the placement service.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ierr := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, ierr)
	}, time.Second*30, time.Millisecond*50)
	assert.Greater(t, r.invoked.Load(), invokedBefore)

	meta, err = gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, "placement: connected", meta.GetActorRuntime().GetPlacement())
	assert.Equal(t, rtv1.ActorRuntime_RUNNING, meta.GetActorRuntime().GetRuntimeStatus())

	// The sidecar's own accounting agrees: the actor is active on this host.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		active := 0
		for _, aa := range r.daprd.GetMetaActorRuntime(c, ctx).ActiveActors {
			if aa.Type == "myactortype" {
				active = aa.Count
			}
		}
		assert.Positive(c, active)
	}, time.Second*10, time.Millisecond*50)
}
