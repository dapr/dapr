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
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reachablegone))
}

// reachablegone covers a scheduler which answered once and then went away.
// The proxy fails the sidecar's jobs streams so the scheduler never
// advertises a leader, and the sidecar waits rather than adopting the
// placement service. Killing the scheduler clears reachability, and only
// then does the next startup timeout adopt the placement service.
type reachablegone struct {
	sched *scheduler.Scheduler
	proxy *proxy.Proxy
	place *placement.Placement
	daprd *daprd.Daprd
	log   *logline.LogLine

	invoked atomic.Int64
}

func (r *reachablegone) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, req *http.Request) {
		r.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	r.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	r.proxy = proxy.New(t, r.sched)
	r.proxy.ArmFailures(proxy.MethodWatchJobs, 1_000_000, codes.Unavailable, nil)
	r.place = placement.New(t)
	r.log = logline.New(t, logline.WithStdoutLineContains(
		"Scheduler reachable but no placement leader advertised",
	))
	r.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(r.proxy.Address()),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithActorsPlacementStartupTimeout(time.Second*3),
		daprd.WithLogLineStdout(r.log),
	)

	return []framework.Option{
		framework.WithProcesses(r.sched, r.proxy, r.place, srv, r.log),
	}
}

func (r *reachablegone) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)

	// The leaderless scheduler broadcasts before the sidecar starts, so the
	// startup timeout cannot beat the first broadcast.
	conn, err := grpc.NewClient(r.proxy.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, serr := schedulerv1pb.NewSchedulerClient(conn).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if !assert.NoError(c, serr) {
			return
		}
		//nolint:errcheck
		defer stream.CloseSend()
		_, serr = stream.Recv()
		assert.NoError(c, serr)
	}, time.Second*20, time.Millisecond*50)

	r.daprd.Run(t, ctx)
	t.Cleanup(func() { r.daprd.Cleanup(t) })

	// A leaderless but reachable scheduler makes the sidecar wait, not
	// defect to the placement service.
	r.log.EventuallyFoundAll(t)

	placementRuntimes := func(c *assert.CollectT) float64 {
		var runtimes float64
		for k, v := range r.place.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
				runtimes += v
			}
		}
		return runtimes
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, placementRuntimes(c))
	}, time.Second*5, time.Millisecond*50)

	// The lost stream clears reachability, so the next timeout adopts the
	// placement service.
	r.sched.Cleanup(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, placementRuntimes(c), float64(1))
	}, time.Second*30, time.Millisecond*50)

	r.daprd.WaitUntilRunning(t, ctx)

	client := r.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*30, time.Millisecond*10)
	assert.Positive(t, r.invoked.Load())
}
