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
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(adopt))
}

// adopt flips the placement authority under a running sidecar in both
// directions, mirroring the helm flag flip, with no sidecar restart.
type adopt struct {
	schedOff  *scheduler.Scheduler
	schedOn   *scheduler.Scheduler
	schedOff2 *scheduler.Scheduler
	place     *placement.Placement
	place2    *placement.Placement
	daprd     *daprd.Daprd

	invoked atomic.Int64
}

func (a *adopt) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {
		a.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	a.schedOff = scheduler.New(t)
	sameIdentity := func(placementEnabled bool) *scheduler.Scheduler {
		opts := []scheduler.Option{
			scheduler.WithID(a.schedOff.ID()),
			scheduler.WithPort(a.schedOff.Port()),
			scheduler.WithEtcdClientPort(a.schedOff.EtcdClientPort()),
			scheduler.WithInitialCluster(a.schedOff.InitialCluster()),
			scheduler.WithDataDir(a.schedOff.DataDir()),
		}
		if placementEnabled {
			opts = append(opts, scheduler.WithPlacementEnabled(true))
		}
		return scheduler.New(t, opts...)
	}
	a.schedOn = sameIdentity(true)
	a.schedOff2 = sameIdentity(false)

	a.place = placement.New(t)
	a.place2 = placement.New(t,
		placement.WithID(a.place.ID()),
		placement.WithPort(a.place.Port()),
		placement.WithInitialCluster(a.place.InitialCluster()),
		placement.WithInitialClusterPorts(a.place.InitialClusterPorts()...),
	)
	a.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(a.schedOff),
		daprd.WithPlacementAddresses(a.place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(a.schedOff, a.place, srv, a.daprd),
	}
}

func (a *adopt) Run(t *testing.T, ctx context.Context) {
	a.schedOff.WaitUntilRunning(t, ctx)
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	gclient := a.daprd.GRPCClient(t, ctx)
	invoke := func() {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "myactortype",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)
		}, time.Second*20, time.Millisecond*10)
	}
	placementRuntimes := func(c *assert.CollectT) float64 {
		var runtimes float64
		for k, v := range a.place.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
				runtimes += v
			}
		}
		return runtimes
	}

	invoke()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, placementRuntimes(c), float64(1))
	}, time.Second*10, time.Millisecond*50)
	invokedBefore := a.invoked.Load()

	// The flag flips on: the sidecar adopts the scheduler placement.
	a.schedOff.Cleanup(t)
	a.place.Cleanup(t)
	a.schedOn.Run(t, ctx)
	t.Cleanup(func() { a.schedOn.Cleanup(t) })
	a.schedOn.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var streams float64
		for k, v := range a.schedOn.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(c, streams, float64(1))
	}, time.Second*30, time.Millisecond*50)
	invoke()
	assert.Greater(t, a.invoked.Load(), invokedBefore)
	invokedBefore = a.invoked.Load()

	// The flag flips off: the sidecar returns to the placement service.
	a.schedOn.Cleanup(t)
	a.schedOff2.Run(t, ctx)
	t.Cleanup(func() { a.schedOff2.Cleanup(t) })
	a.place2.Run(t, ctx)
	t.Cleanup(func() { a.place2.Cleanup(t) })
	a.schedOff2.WaitUntilRunning(t, ctx)
	a.place2.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var runtimes float64
		for k, v := range a.place2.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
				runtimes += v
			}
		}
		assert.GreaterOrEqual(c, runtimes, float64(1))
	}, time.Second*30, time.Millisecond*50)
	invoke()
	assert.Greater(t, a.invoked.Load(), invokedBefore)
}
