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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(authoritydown))
}

// authoritydown runs the identical outage against both placement
// authorities: the authority is killed mid-test, invocation stalls, the
// authority returns on the same address and actor calls flow again with no
// sidecar restart. A divergence in what an outage costs a running actor
// host fails one leg and not the other.
type authoritydown struct {
	place *downTopology
	sched *downTopology
}

// downTopology is one actor host placed by one authority, with hooks to
// kill that authority and bring it back on the same address.
type downTopology struct {
	daprd   *daprd.Daprd
	invoked atomic.Int64
	kill    func(t *testing.T)
	revive  func(t *testing.T, ctx context.Context)
}

func (a *authoritydown) newTopology(t *testing.T) (*downTopology, *prochttp.HTTP) {
	t.Helper()
	topo := new(downTopology)
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {
		topo.invoked.Add(1)
	})
	return topo, prochttp.New(t, prochttp.WithHandler(handler))
}

func (a *authoritydown) Setup(t *testing.T) []framework.Option {
	// Placement leg: the scheduler does not serve placement, the
	// pre-PlacementV2 topology.
	placeTopo, placeSrv := a.newTopology(t)
	placeSched := scheduler.New(t)
	place := placement.New(t)
	placeBack := placement.New(t,
		placement.WithID(place.ID()),
		placement.WithPort(place.Port()),
		placement.WithInitialCluster(place.InitialCluster()),
		placement.WithInitialClusterPorts(place.InitialClusterPorts()...),
	)
	placeTopo.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(placeSrv.Port()),
		daprd.WithScheduler(placeSched),
		daprd.WithPlacementAddresses(place.Address()),
	)
	placeTopo.kill = func(t *testing.T) { place.Cleanup(t) }
	placeTopo.revive = func(t *testing.T, ctx context.Context) {
		placeBack.Run(t, ctx)
		t.Cleanup(func() { placeBack.Cleanup(t) })
		placeBack.WaitUntilRunning(t, ctx)
	}
	a.place = placeTopo

	// Scheduler leg: the scheduler serves placement. Its replacement reuses
	// the address, ID and data directory, standing in for the scheduler
	// being rescheduled.
	schedTopo, schedSrv := a.newTopology(t)
	sched := scheduler.New(t, scheduler.WithPlacementEnabled(true))
	schedBack := scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithID(sched.ID()),
		scheduler.WithPort(sched.Port()),
		scheduler.WithEtcdClientPort(sched.EtcdClientPort()),
		scheduler.WithInitialCluster(sched.InitialCluster()),
		scheduler.WithDataDir(sched.DataDir()),
	)
	schedTopo.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(schedSrv.Port()),
		daprd.WithScheduler(sched),
	)
	schedTopo.kill = func(t *testing.T) { sched.Kill(t) }
	schedTopo.revive = func(t *testing.T, ctx context.Context) {
		schedBack.Run(t, ctx)
		t.Cleanup(func() { schedBack.Cleanup(t) })
		schedBack.WaitUntilRunning(t, ctx)
	}
	a.sched = schedTopo

	return []framework.Option{
		framework.WithProcesses(
			placeSched, place, placeSrv, placeTopo.daprd,
			sched, schedSrv, schedTopo.daprd,
		),
	}
}

func (a *authoritydown) Run(t *testing.T, ctx context.Context) {
	t.Run("placement", func(t *testing.T) { a.body(t, ctx, a.place) })
	t.Run("scheduler", func(t *testing.T) { a.body(t, ctx, a.sched) })
}

func (a *authoritydown) body(t *testing.T, ctx context.Context, topo *downTopology) {
	topo.daprd.WaitUntilRunning(t, ctx)

	gclient := topo.daprd.GRPCClient(t, ctx)

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
	assert.Equal(t, "placement: connected", meta.GetActorRuntime().GetPlacement())
	assert.Equal(t, rtv1.ActorRuntime_RUNNING, meta.GetActorRuntime().GetRuntimeStatus())

	invokedBefore := topo.invoked.Load()
	topo.kill(t)

	// The sidecar reports the placement connection as lost.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		if !assert.NoError(c, merr) {
			return
		}
		assert.Equal(c, "placement: disconnected", meta.GetActorRuntime().GetPlacement())
	}, time.Second*20, time.Millisecond*10)

	// Actor invocation stalls while placement is unavailable, including for
	// an actor which was already active on this very host: the sidecar will
	// not route from a table it can no longer trust.
	stallCtx, cancel := context.WithTimeout(ctx, time.Second*3)
	t.Cleanup(cancel)
	_, err = gclient.InvokeActor(stallCtx, &rtv1.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Method:    "foo",
	})
	require.Error(t, err, "actor invocation should stall while placement is down")
	assert.Equal(t, invokedBefore, topo.invoked.Load(), "no call should have reached the app")

	topo.revive(t, ctx)

	// Placement recovers and actor calls flow again, without restarting the
	// sidecar and without any persisted placement state: the table is
	// rebuilt from the sidecar's stream.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		if !assert.NoError(c, merr) {
			return
		}
		assert.Equal(c, "placement: connected", meta.GetActorRuntime().GetPlacement())
	}, time.Second*30, time.Millisecond*10)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ierr := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, ierr)
	}, time.Second*20, time.Millisecond*10)
	assert.Greater(t, topo.invoked.Load(), invokedBefore)
}
