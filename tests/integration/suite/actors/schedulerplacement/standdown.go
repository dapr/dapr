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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(standdown))
}

// standdown tests the handoff handshake end to end: the announced placement
// service drains on the cutover signal, halting every actor with a final
// empty table before refusing streams and confirming. Only then does the
// scheduler advertise the placement leader, so two placement authorities
// never serve at once.
type standdown struct {
	sched *scheduler.Scheduler
	place *placement.Placement
}

func (s *standdown) Setup(t *testing.T) []framework.Option {
	s.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	s.place = placement.New(t,
		placement.WithSchedulerAddresses(s.sched.Address()),
		placement.WithDisseminateTimeout(time.Second*5),
	)

	return []framework.Option{
		framework.WithProcesses(s.sched, s.place),
	}
}

func (s *standdown) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)

	// An old sidecar's jobs stream holds the capability gate: the scheduler
	// does not advertise placement leadership, so the placement service must
	// keep serving.
	oldCtx, oldCancel := context.WithCancel(ctx)
	t.Cleanup(oldCancel)
	s.sched.WatchJobsSuccess(t, oldCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "old-sidecar",
		Namespace: "default",
	})

	// A capable sidecar's stream, since the advertisement and the cutover
	// signal both wait for one to exist.
	s.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "new-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})

	// A placement stream, as the old sidecar would open one, is accepted and
	// receives its first placement order.
	client := s.place.Client(t, ctx)
	var stream placementv1pb.Placement_ReportDaprStatusClient
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		st, err := client.ReportDaprStatus(ctx)
		if !assert.NoError(c, err) {
			return
		}
		//nolint:errcheck
		st.Send(&placementv1pb.Host{
			Name:      "127.0.0.1:40001",
			Id:        "old-app",
			Namespace: "default",
			Entities:  []string{"myactortype"},
			ApiLevel:  20,
		})
		if _, err = st.Recv(); !assert.NoError(c, err,
			"placement must serve while the scheduler does not advertise placement") {
			return
		}
		stream = st
	}, time.Second*30, time.Millisecond*50)

	// The placement service announced itself, so even without the gate the
	// scheduler must not advertise a placement leader before the stand-down
	// confirmation.
	hostsStream, err := s.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
	require.NoError(t, err)
	resp, err := hostsStream.Recv()
	require.NoError(t, err)
	for _, host := range resp.GetHosts() {
		assert.False(t, host.GetLeader(),
			"no scheduler placement leader may be advertised while the placement service serves")
	}

	// The old sidecar's jobs stream closes: the gate lifts and the scheduler
	// signals the pending cutover. The placement service drains: the held
	// stream receives a final empty table, halting its actors, then closes.
	oldCancel()

	sawEmptyTable := false
	drainCtx, drainCancel := context.WithTimeout(ctx, time.Second*30)
	defer drainCancel()
	for {
		order, rerr := stream.Recv()
		if rerr != nil {
			break
		}
		require.NoError(t, drainCtx.Err(), "placement stream was not drained in time")
		if order.GetOperation() == "update" {
			assert.Empty(t, order.GetTables().GetEntries(),
				"the drain must deliver an empty table so the sidecar halts every actor")
			sawEmptyTable = true
		}
		// An old client's report counts as the acknowledgement for the
		// current dissemination phase.
		//nolint:errcheck
		stream.Send(&placementv1pb.Host{
			Name:      "127.0.0.1:40001",
			Id:        "old-app",
			Namespace: "default",
			Entities:  []string{"myactortype"},
			ApiLevel:  20,
		})
	}
	assert.True(t, sawEmptyTable, "the drain round did not reach the sidecar before the stream closed")

	// New placement streams are refused with a reason.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		refused, serr := client.ReportDaprStatus(ctx)
		if !assert.NoError(c, serr) {
			return
		}
		//nolint:errcheck
		refused.Send(&placementv1pb.Host{
			Name:      "127.0.0.1:40001",
			Id:        "old-app",
			Namespace: "default",
			Entities:  []string{"myactortype"},
			ApiLevel:  20,
		})
		_, rerr := refused.Recv()
		if !assert.Error(c, rerr) {
			return
		}
		assert.Equal(c, codes.FailedPrecondition, status.Code(rerr))
		assert.Contains(c, status.Convert(rerr).Message(), "standing down")
	}, time.Second*30, time.Millisecond*50)

	// Only after the confirmed stand-down does the scheduler advertise the
	// placement leader.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		hs, herr := s.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if !assert.NoError(c, herr) {
			return
		}
		//nolint:errcheck
		defer hs.CloseSend()
		hresp, herr := hs.Recv()
		if !assert.NoError(c, herr) {
			return
		}
		leader := false
		for _, host := range hresp.GetHosts() {
			leader = leader || host.GetLeader()
		}
		assert.True(c, leader, "the placement leader must be advertised once the placement service confirmed its stand-down")
	}, time.Second*30, time.Millisecond*50)
}
