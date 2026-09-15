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
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bothalive))
}

// bothalive runs the placement service next to a scheduler cluster serving
// placement: the sidecar follows the advertisement through a leader loss
// and the placement service never sees a host.
type bothalive struct {
	cluster *cluster.Cluster
	place   *placement.Placement
	daprd   *daprd.Daprd

	invoked atomic.Int64
}

func (b *bothalive) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
		b.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	b.cluster = cluster.New(t,
		cluster.WithCount(3),
		cluster.WithSchedulerOptions(scheduler.WithPlacementEnabled(true)),
	)
	b.place = placement.New(t)
	b.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(b.cluster.Addresses()...),
		daprd.WithPlacementAddresses(b.place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(b.cluster, b.place, srv, b.daprd),
	}
}

func (b *bothalive) Run(t *testing.T, ctx context.Context) {
	b.cluster.WaitUntilRunning(t, ctx)
	b.place.WaitUntilRunning(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)
	invoke := func() {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "myactortype",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)
		}, time.Second*30, time.Millisecond*10)
	}
	placementRuntimes := func(c *assert.CollectT) float64 {
		var runtimes float64
		for k, v := range b.place.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
				runtimes += v
			}
		}
		return runtimes
	}

	invoke()

	// The advertised placement leader dies: invocations recover on the new
	// leader without the sidecar defecting to the placement service.
	var leaderAddr string
	sclient := b.cluster.Client(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err := sclient.WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return
		}
		for _, host := range resp.GetHosts() {
			if host.GetLeader() {
				leaderAddr = host.GetAddress()
			}
		}
		assert.NotEmpty(c, leaderAddr)
	}, time.Second*20, time.Millisecond*50)

	leaderN := -1
	for n, addr := range b.cluster.Addresses() {
		if addr == leaderAddr {
			leaderN = n
		}
	}
	require.NotEqual(t, -1, leaderN)
	b.cluster.SchedulerN(t, leaderN).Cleanup(t)

	invokedBefore := b.invoked.Load()
	invoke()
	assert.Greater(t, b.invoked.Load(), invokedBefore)

	// The split brain guard: through startup and the leader loss, the
	// placement service never saw a host.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, placementRuntimes(c))
	}, time.Second*10, time.Millisecond*50)
}
