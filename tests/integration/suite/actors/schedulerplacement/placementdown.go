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
	suite.Register(new(placementdown))
}

// placementdown kills the standalone placement service under a running actor
// host: invocation stalls, the service returns on the same address, and
// actor calls flow again with no sidecar restart
type placementdown struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	placeBack *placement.Placement
	invoked   atomic.Int64
}

func (p *placementdown) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {
		p.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	sched := scheduler.New(t)
	p.place = placement.New(t)
	p.placeBack = placement.New(t,
		placement.WithID(p.place.ID()),
		placement.WithPort(p.place.Port()),
		placement.WithInitialCluster(p.place.InitialCluster()),
		placement.WithInitialClusterPorts(p.place.InitialClusterPorts()...),
	)
	p.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(sched),
		daprd.WithPlacementAddresses(p.place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sched, p.place, srv, p.daprd),
	}
}

func (p *placementdown) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)

	gclient := p.daprd.GRPCClient(t, ctx)

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

	invokedBefore := p.invoked.Load()
	p.place.Cleanup(t)

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
	assert.Equal(t, invokedBefore, p.invoked.Load(), "no call should have reached the app")

	// The placement service returns on the same address.
	p.placeBack.Run(t, ctx)
	t.Cleanup(func() { p.placeBack.Cleanup(t) })
	p.placeBack.WaitUntilRunning(t, ctx)

	// Actor calls flow again with no sidecar restart and no persisted
	// placement state: the table is rebuilt from the sidecar's stream.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, merr := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		if !assert.NoError(c, merr) {
			return
		}
		assert.Equal(c, "placement: connected", meta.GetActorRuntime().GetPlacement())
	}, time.Second*20, time.Millisecond*10)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ierr := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, ierr)
	}, time.Second*20, time.Millisecond*10)
	assert.Greater(t, p.invoked.Load(), invokedBefore)
}
