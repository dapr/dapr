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
	suite.Register(new(rollback))
}

// rollback asserts the scheduler placement authority hands back to the placement
// service without restarting sidecars once the control plane rolls back.
// The running sidecar defects only on the explicit no-placement-served
// signal and only to a placement service which accepts it, so a single
// authority holds throughout.
type rollback struct {
	daprd *daprd.Daprd
	// schedPlacement serves placement. schedRolledBack replaces it on the
	// same address with placement disabled, standing in for restarting the
	// scheduler with --placement-enabled=false.
	schedPlacement  *scheduler.Scheduler
	schedRolledBack *scheduler.Scheduler
	place           *placement.Placement
	placeRestarted  *placement.Placement

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

	r.schedPlacement = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	// The placement service is wired for the stand-down handshake, so the
	// cutover completes and the scheduler serves placement.
	r.place = placement.New(t,
		placement.WithSchedulerAddresses(r.schedPlacement.Address()),
	)

	// The rolled back scheduler reuses the same client address, ID and data
	// directory, so from the sidecar's point of view the same scheduler came
	// back with placement disabled.
	r.schedRolledBack = scheduler.New(t,
		scheduler.WithPlacementEnabled(false),
		scheduler.WithID(r.schedPlacement.ID()),
		scheduler.WithPort(r.schedPlacement.Port()),
		scheduler.WithEtcdClientPort(r.schedPlacement.EtcdClientPort()),
		scheduler.WithInitialCluster(r.schedPlacement.InitialCluster()),
		scheduler.WithDataDir(r.schedPlacement.DataDir()),
	)

	// The restarted placement service reuses the original address, standing
	// in for the control plane restart which completes the rollback.
	r.placeRestarted = placement.New(t,
		placement.WithPort(r.place.Port()),
		placement.WithSchedulerAddresses(r.schedPlacement.Address()),
	)

	r.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(r.schedPlacement),
		daprd.WithPlacementAddresses(r.place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(r.schedPlacement, r.place, srv, r.daprd),
	}
}

func (r *rollback) Run(t *testing.T, ctx context.Context) {
	r.schedPlacement.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	// The placement service stands down through the handshake, then the
	// scheduler advertises the placement leader.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, serr := r.schedPlacement.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
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

	// Actors are placed by the scheduler: the placement service has stood
	// down and refuses streams, so working actors prove it.
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

	// The scheduler holds the sidecar's placement stream before the
	// rollback, and the stood-down placement service holds none: the
	// metrics prove a single placement authority.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var streams float64
		for k, v := range r.schedPlacement.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(c, streams, float64(1))

		var runtimes float64
		var leader float64
		for k, v := range r.place.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
				runtimes += v
			}
		}
		assert.Zero(c, runtimes)

		for k, v := range r.schedPlacement.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_leader") {
				leader += v
			}
		}
		assert.Equal(c, 1, int(leader))
	}, time.Second*10, time.Millisecond*50)

	invokedBefore := r.invoked.Load()

	// Roll back the control plane: the scheduler returns with placement
	// disabled and the placement service returns serving.
	r.schedPlacement.Cleanup(t)
	r.place.Cleanup(t)
	r.schedRolledBack.Run(t, ctx)
	t.Cleanup(func() { r.schedRolledBack.Cleanup(t) })
	r.schedRolledBack.WaitUntilRunning(t, ctx)
	r.placeRestarted.Run(t, ctx)
	t.Cleanup(func() { r.placeRestarted.Cleanup(t) })
	r.placeRestarted.WaitUntilRunning(t, ctx)

	// The running sidecar adopts the serving placement service without a
	// restart: the rolled back scheduler reports no placement served, and
	// the placement service accepts it.
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

	// The placement service holds the sidecar after the rollback, and the
	// rolled back scheduler holds no placement stream: the authority moved
	// back whole.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var runtimes float64
		for k, v := range r.placeRestarted.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
				runtimes += v
			}
		}
		assert.GreaterOrEqual(c, runtimes, float64(1))

		var streams float64
		var leader float64
		for k, v := range r.schedRolledBack.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
			if strings.HasPrefix(k, "dapr_scheduler_placement_leader") {
				leader += v
			}
		}
		assert.Zero(c, streams)
		assert.Zero(c, leader)
	}, time.Second*10, time.Millisecond*50)

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
