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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hangingplacement))
}

// hangingplacement reports a placement service which accepts connections, but
// never answers the protocol check: a reachable placement service is a
// placement service regardless of how slowly it answers, so the leader stays
// withheld until it is gone.
type hangingplacement struct {
	sched *scheduler.Scheduler
	srv   *grpc.Server
	addr  string
}

type hangingReportServer struct {
	v1pb.UnimplementedPlacementServer
}

func (h *hangingReportServer) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (h *hangingplacement) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	lis := ports.Reserve(t, 1).Listener(t)
	h.addr = lis.Addr().String()
	h.srv = grpc.NewServer()
	v1pb.RegisterPlacementServer(h.srv, &hangingReportServer{})
	go h.srv.Serve(lis)
	t.Cleanup(h.srv.Stop)

	h.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))

	return []framework.Option{
		framework.WithProcesses(h.sched),
	}
}

func (h *hangingplacement) Run(t *testing.T, ctx context.Context) {
	h.sched.WaitUntilRunning(t, ctx)

	h.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
		PlacementAddresses:         []string{h.addr},
	})

	leader := func() (leader, capable bool, ok bool) {
		stream, err := h.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return false, false, false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return false, false, false
		}
		for _, host := range resp.GetHosts() {
			leader = leader || host.GetLeader()
			capable = capable || host.GetSchedulerPlacementEnabled()
		}
		return leader, capable, true
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		l, capable, ok := leader()
		if assert.True(c, ok) {
			assert.False(c, l)
			assert.False(c, capable)
		}
	}, time.Second*10, time.Millisecond*10)

	require.Never(t, func() bool {
		l, _, ok := leader()
		return ok && l
	}, time.Second*10, time.Millisecond*10,
		"a placement service which hangs on the protocol check is still present")

	// Stopping the hanging placement service confirms its absence and hands
	// placement to the scheduler.
	h.srv.Stop()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		l, _, ok := leader()
		assert.True(c, ok && l)
	}, time.Second*15, time.Millisecond*10)
}
