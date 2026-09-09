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

package placement

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(restart))
}

// restart asserts a scheduler shutting down closes its live placement
// streams with Unavailable telling the sidecar placement is shutting down,
// and a reconnect after the restart is served a fresh snapshot.
type restart struct {
	sched     *scheduler.Scheduler
	schedBack *scheduler.Scheduler
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	r.schedBack = scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithID(r.sched.ID()),
		scheduler.WithPort(r.sched.Port()),
		scheduler.WithEtcdClientPort(r.sched.EtcdClientPort()),
		scheduler.WithInitialCluster(r.sched.InitialCluster()),
		scheduler.WithDataDir(r.sched.DataDir()),
	)
	return []framework.Option{
		framework.WithProcesses(r.sched),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)

	sendAck := func(stream schedulerv1pb.Scheduler_ReportActorTypesClient, order *schedulerv1pb.PlacementOrder) error {
		return stream.Send(&schedulerv1pb.ReportActorTypesRequest{
			Msg: &schedulerv1pb.ReportActorTypesRequest_Ack{Ack: &schedulerv1pb.PlacementOrderAck{
				Operation: order.GetOperation(),
				Seq:       order.GetSeq(),
			}},
		})
	}

	openAndReport := func(sched *scheduler.Scheduler) schedulerv1pb.Scheduler_ReportActorTypesClient {
		var stream schedulerv1pb.Scheduler_ReportActorTypesClient
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			stream, err = sched.Client(t, ctx).ReportActorTypes(ctx)
			if !assert.NoError(c, err) {
				return
			}
			err = stream.Send(&schedulerv1pb.ReportActorTypesRequest{
				Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: &schedulerv1pb.ActorHost{
					Address:    "127.0.0.1:40001",
					AppId:      "myapp",
					Namespace:  "default",
					ActorTypes: []string{"mytype"},
				}},
			})
			if !assert.NoError(c, err) {
				return
			}
			order, err := stream.Recv()
			if !assert.NoError(c, err) {
				return
			}
			assert.NoError(c, sendAck(stream, order))
			assert.Equal(c, schedulerv1pb.Operation_OPERATION_LOCK, order.GetOperation())
		}, time.Second*20, time.Millisecond*100)
		return stream
	}

	jobsCtx, jobsCancel := context.WithCancel(ctx)
	r.sched.WatchJobsSuccess(t, jobsCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})
	stream := openAndReport(r.sched)

	// The scheduler shuts down with the stream live: the stream is closed
	// telling the sidecar placement is shutting down.
	jobsCancel()
	r.sched.Cleanup(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := stream.Recv()
		if !assert.Error(c, err) {
			return
		}
		assert.Equal(c, codes.Unavailable, status.Code(err))
		assert.ErrorContains(c, err, "placement is shutting down")
	}, time.Second*20, time.Millisecond*100)

	// The scheduler restarts on the same address and data directory: a
	// reconnect is served a fresh snapshot.
	r.schedBack.Run(t, ctx)
	t.Cleanup(func() { r.schedBack.Cleanup(t) })
	r.schedBack.WaitUntilRunning(t, ctx)

	r.schedBack.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})
	stream = openAndReport(r.schedBack)
	for {
		order, err := stream.Recv()
		require.NoError(t, err)
		require.NoError(t, sendAck(stream, order))
		if order.GetOperation() != schedulerv1pb.Operation_OPERATION_UPDATE {
			continue
		}
		table, ok := order.GetTables().GetEntries()["mytype"]
		if !ok {
			continue
		}
		require.Len(t, table.GetHosts(), 1)
		require.Contains(t, table.GetHosts(), "127.0.0.1:40001")
		break
	}
}
