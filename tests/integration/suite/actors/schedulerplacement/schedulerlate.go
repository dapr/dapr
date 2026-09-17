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
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(schedulerlate))
}

// schedulerlate boots a placement enabled scheduler into a cluster already
// served by a healthy placement service: the ready gate holds the leader
// until the first detection, which finds the placement service, so the
// restarting scheduler never yanks the sidecar off it.
type schedulerlate struct {
	sched *scheduler.Scheduler
	place *placement.Placement
	daprd *daprd.Daprd

	invoked atomic.Int64
}

func (s *schedulerlate) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
		s.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	s.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithActorsPlacementStartupTimeout(time.Second*3),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, srv, s.daprd),
	}
}

func (s *schedulerlate) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*15, time.Millisecond*10)

	// The scheduler arrives late into a cluster the placement service
	// already serves.
	s.sched.Run(t, ctx)
	t.Cleanup(func() { s.sched.Cleanup(t) })
	s.sched.WaitUntilRunning(t, ctx)

	require.Never(t, func() bool {
		stream, err := s.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
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
	}, time.Second*15, time.Millisecond*10,
		"a scheduler booting into a live placement service must not advertise")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var runtimes float64
		for k, v := range s.place.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
				runtimes += v
			}
		}
		assert.GreaterOrEqual(c, runtimes, float64(1))
	}, time.Second*10, time.Millisecond*10)

	invokedBefore := s.invoked.Load()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)
	assert.Greater(t, s.invoked.Load(), invokedBefore)
}
