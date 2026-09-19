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
	suite.Register(new(rereport))
}

// rereport asserts a host re-reporting on its existing stream replaces its
// hosted actor types: the new type gains the host and the dropped type is
// removed from the tables.
type rereport struct {
	sched *scheduler.Scheduler
}

func (r *rereport) Setup(t *testing.T) []framework.Option {
	r.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	return []framework.Option{
		framework.WithProcesses(r.sched),
	}
}

func (r *rereport) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)

	r.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})

	sendAck := func(stream schedulerv1pb.Scheduler_ReportActorTypesClient, order *schedulerv1pb.PlacementOrder) error {
		return stream.Send(&schedulerv1pb.ReportActorTypesRequest{
			Msg: &schedulerv1pb.ReportActorTypesRequest_Ack{Ack: &schedulerv1pb.PlacementOrderAck{
				Operation: order.GetOperation(),
				Seq:       order.GetSeq(),
			}},
		})
	}

	var stream schedulerv1pb.Scheduler_ReportActorTypesClient
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		stream, err = r.sched.Client(t, ctx).ReportActorTypes(ctx)
		if !assert.NoError(c, err) {
			return
		}
		err = stream.Send(&schedulerv1pb.ReportActorTypesRequest{
			Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: &schedulerv1pb.ActorHost{
				Address:    "127.0.0.1:40001",
				AppId:      "myapp",
				Namespace:  "default",
				ActorTypes: []string{"typeA"},
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

	hostsOf := func(order *schedulerv1pb.PlacementOrder, actorType string) []string {
		table, ok := order.GetTables().GetEntries()[actorType]
		if !ok {
			return nil
		}
		addrs := make([]string, 0, len(table.GetHosts()))
		for addr := range table.GetHosts() {
			addrs = append(addrs, addr)
		}
		return addrs
	}

	ackOrdersUntil := func(match func(*schedulerv1pb.PlacementOrder) bool) {
		for {
			order, err := stream.Recv()
			require.NoError(t, err)
			require.NoError(t, sendAck(stream, order))
			if match(order) {
				return
			}
		}
	}

	ackOrdersUntil(func(order *schedulerv1pb.PlacementOrder) bool {
		return order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE &&
			len(hostsOf(order, "typeA")) == 1
	})

	// The host swaps its hosted type on the same stream.
	require.NoError(t, stream.Send(&schedulerv1pb.ReportActorTypesRequest{
		Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: &schedulerv1pb.ActorHost{
			Address:    "127.0.0.1:40001",
			AppId:      "myapp",
			Namespace:  "default",
			ActorTypes: []string{"typeB"},
		}},
	}))

	var gainedTypeB, removedTypeA bool
	ackOrdersUntil(func(order *schedulerv1pb.PlacementOrder) bool {
		if order.GetOperation() != schedulerv1pb.Operation_OPERATION_UPDATE {
			return false
		}
		if hosts := hostsOf(order, "typeB"); len(hosts) == 1 {
			assert.ElementsMatch(t, []string{"127.0.0.1:40001"}, hosts)
			gainedTypeB = true
		}
		if typeA, ok := order.GetTables().GetEntries()["typeA"]; ok && len(typeA.GetHosts()) == 0 {
			removedTypeA = true
		}
		return gainedTypeB && removedTypeA
	})
}
