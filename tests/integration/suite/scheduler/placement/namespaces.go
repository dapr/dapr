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

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(namespaces))
}

// namespaces asserts placement tables are namespaced: hosts reporting the
// same actor type in different namespaces never appear in each other's
// tables.
type namespaces struct {
	sched *scheduler.Scheduler
}

func (n *namespaces) Setup(t *testing.T) []framework.Option {
	n.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	return []framework.Option{
		framework.WithProcesses(n.sched),
	}
}

func (n *namespaces) Run(t *testing.T, ctx context.Context) {
	n.sched.WaitUntilRunning(t, ctx)

	n.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})

	// openAndReport opens a ReportActorTypes stream and sends the host
	// report, retrying while the leader election settles.
	openAndReport := func(host *schedulerv1pb.ActorHost) schedulerv1pb.Scheduler_ReportActorTypesClient {
		var stream schedulerv1pb.Scheduler_ReportActorTypesClient
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			stream, err = n.sched.Client(t, ctx).ReportActorTypes(ctx)
			if !assert.NoError(c, err) {
				return
			}
			err = stream.Send(&schedulerv1pb.ReportActorTypesRequest{
				Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: host},
			})
			if !assert.NoError(c, err) {
				return
			}
			order, err := stream.Recv()
			if !assert.NoError(c, err) {
				return
			}
			assert.NoError(c, stream.Send(&schedulerv1pb.ReportActorTypesRequest{
				Msg: &schedulerv1pb.ReportActorTypesRequest_Ack{Ack: &schedulerv1pb.PlacementOrderAck{
					Operation: order.GetOperation(),
					Seq:       order.GetSeq(),
				}},
			}))
			assert.Equal(c, schedulerv1pb.Operation_OPERATION_LOCK, order.GetOperation())
		}, time.Second*20, time.Millisecond*100)
		return stream
	}

	sharedHosts := func(order *schedulerv1pb.PlacementOrder) []string {
		table, ok := order.GetTables().GetEntries()["shared"]
		if !ok {
			return nil
		}
		addrs := make([]string, 0, len(table.GetHosts()))
		for addr := range table.GetHosts() {
			addrs = append(addrs, addr)
		}
		return addrs
	}

	// ackOrdersUntilUpdate acknowledges every order until the update
	// carrying the shared type's table arrives, returning it.
	ackOrdersUntilUpdate := func(stream schedulerv1pb.Scheduler_ReportActorTypesClient) *schedulerv1pb.PlacementOrder {
		for {
			order, err := stream.Recv()
			require.NoError(t, err)
			require.NoError(t, stream.Send(&schedulerv1pb.ReportActorTypesRequest{
				Msg: &schedulerv1pb.ReportActorTypesRequest_Ack{Ack: &schedulerv1pb.PlacementOrderAck{
					Operation: order.GetOperation(),
					Seq:       order.GetSeq(),
				}},
			}))
			if order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE &&
				len(sharedHosts(order)) > 0 {
				return order
			}
		}
	}

	streamA := openAndReport(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40001",
		AppId:      "app-a",
		Namespace:  "ns1",
		ActorTypes: []string{"shared"},
	})
	updateA := ackOrdersUntilUpdate(streamA)
	assert.Equal(t, "ns1", updateA.GetNamespace())
	assert.ElementsMatch(t, []string{"127.0.0.1:40001"}, sharedHosts(updateA))

	// The second namespace's host gets a table with only itself, however
	// both report the same actor type.
	streamB := openAndReport(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40002",
		AppId:      "app-b",
		Namespace:  "ns2",
		ActorTypes: []string{"shared"},
	})
	updateB := ackOrdersUntilUpdate(streamB)
	assert.Equal(t, "ns2", updateB.GetNamespace())
	assert.ElementsMatch(t, []string{"127.0.0.1:40002"}, sharedHosts(updateB))
}
