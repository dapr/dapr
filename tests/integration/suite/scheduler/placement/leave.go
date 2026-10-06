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
	suite.Register(new(leave))
}

// leave asserts a host leaving rebalances the survivors: the survivor
// receives a round dropping that host, a type whose last host left is
// removed from the tables, and a stream closing mid-round without acking
// still lets the round complete for the survivors.
type leave struct {
	sched *scheduler.Scheduler
}

func (l *leave) Setup(t *testing.T) []framework.Option {
	l.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	return []framework.Option{
		framework.WithProcesses(l.sched),
	}
}

func (l *leave) Run(t *testing.T, ctx context.Context) {
	l.sched.WaitUntilRunning(t, ctx)

	l.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
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

	openAndReport := func(ctx context.Context, host *schedulerv1pb.ActorHost) schedulerv1pb.Scheduler_ReportActorTypesClient {
		var stream schedulerv1pb.Scheduler_ReportActorTypesClient
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			stream, err = l.sched.Client(t, ctx).ReportActorTypes(ctx)
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
			assert.NoError(c, sendAck(stream, order))
			assert.Equal(c, schedulerv1pb.Operation_OPERATION_LOCK, order.GetOperation())
		}, time.Second*20, time.Millisecond*100)
		return stream
	}

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

	ackOrdersUntil := func(stream schedulerv1pb.Scheduler_ReportActorTypesClient, match func(*schedulerv1pb.PlacementOrder) bool) *schedulerv1pb.PlacementOrder {
		for {
			order, err := stream.Recv()
			require.NoError(t, err)
			require.NoError(t, sendAck(stream, order))
			if match(order) {
				return order
			}
		}
	}

	streamA := openAndReport(ctx, &schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40001",
		AppId:      "app-a",
		Namespace:  "default",
		ActorTypes: []string{"typeA"},
	})

	// The leaver hosts typeA alongside the survivor, and typeB alone.
	bctx, bcancel := context.WithCancel(ctx)
	streamB := openAndReport(bctx, &schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40002",
		AppId:      "app-b",
		Namespace:  "default",
		ActorTypes: []string{"typeA", "typeB"},
	})

	bothJoined := func(order *schedulerv1pb.PlacementOrder) bool {
		return order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE &&
			len(hostsOf(order, "typeA")) == 2
	}
	errB := make(chan error, 1)
	go func() {
		for {
			order, err := streamB.Recv()
			if err != nil {
				errB <- err
				return
			}
			if err = sendAck(streamB, order); err != nil {
				errB <- err
				return
			}
		}
	}()
	ackOrdersUntil(streamA, bothJoined)

	// The leaver goes away: the survivor receives rounds dropping it from
	// typeA and removing typeB, whose last host left.
	bcancel()
	var droppedFromTypeA, removedTypeB bool
	ackOrdersUntil(streamA, func(order *schedulerv1pb.PlacementOrder) bool {
		if order.GetOperation() != schedulerv1pb.Operation_OPERATION_UPDATE {
			return false
		}
		if hosts := hostsOf(order, "typeA"); len(hosts) == 1 {
			assert.ElementsMatch(t, []string{"127.0.0.1:40001"}, hosts)
			droppedFromTypeA = true
		}
		if typeB, ok := order.GetTables().GetEntries()["typeB"]; ok && len(typeB.GetHosts()) == 0 {
			removedTypeB = true
		}
		return droppedFromTypeA && removedTypeB
	})
	<-errB

	// A third host joins, and mid-round the survivor of the last round
	// closes without acking: the round still completes for the remaining
	// streams.
	cctx, ccancel := context.WithCancel(ctx)
	t.Cleanup(ccancel)
	streamC := openAndReport(cctx, &schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40003",
		AppId:      "app-c",
		Namespace:  "default",
		ActorTypes: []string{"typeA"},
	})

	dctx, dcancel := context.WithCancel(ctx)
	streamD := openAndReport(dctx, &schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40004",
		AppId:      "app-d",
		Namespace:  "default",
		ActorTypes: []string{"typeA"},
	})

	// The joining fifth host starts a round to A, C and D. D receives its
	// LOCK and closes without acking.
	ectx, ecancel := context.WithCancel(ctx)
	t.Cleanup(ecancel)
	streamE := openAndReport(ectx, &schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40005",
		AppId:      "app-e",
		Namespace:  "default",
		ActorTypes: []string{"typeA"},
	})
	_, err := streamD.Recv()
	require.NoError(t, err)
	dcancel()

	converged := func(order *schedulerv1pb.PlacementOrder) bool {
		if order.GetOperation() != schedulerv1pb.Operation_OPERATION_UPDATE {
			return false
		}
		hosts := hostsOf(order, "typeA")
		return len(hosts) == 3
	}
	errC := make(chan error, 1)
	var updateC *schedulerv1pb.PlacementOrder
	go func() {
		for {
			order, cerr := streamC.Recv()
			if cerr != nil {
				errC <- cerr
				return
			}
			if cerr = sendAck(streamC, order); cerr != nil {
				errC <- cerr
				return
			}
			if converged(order) {
				updateC = order
				errC <- nil
				return
			}
		}
	}()
	errE := make(chan error, 1)
	go func() {
		for {
			order, eerr := streamE.Recv()
			if eerr != nil {
				errE <- eerr
				return
			}
			if eerr = sendAck(streamE, order); eerr != nil {
				errE <- eerr
				return
			}
			if converged(order) {
				errE <- nil
				return
			}
		}
	}()
	updateA := ackOrdersUntil(streamA, converged)
	require.NoError(t, <-errC)
	require.NoError(t, <-errE)
	want := []string{"127.0.0.1:40001", "127.0.0.1:40003", "127.0.0.1:40005"}
	assert.ElementsMatch(t, want, hostsOf(updateA, "typeA"))
	assert.ElementsMatch(t, want, hostsOf(updateC, "typeA"))
}
