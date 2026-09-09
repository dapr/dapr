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
	suite.Register(new(coalesce))
}

// coalesce asserts membership churn within the coalesce window collapses
// into one dissemination round: 2 hosts joining back to back land on the
// observer in a single update, never one at a time.
type coalesce struct {
	sched *scheduler.Scheduler
}

func (o *coalesce) Setup(t *testing.T) []framework.Option {
	o.sched = scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithPlacementDisseminateCoalesceWindow(time.Second*8),
	)
	return []framework.Option{
		framework.WithProcesses(o.sched),
	}
}

func (o *coalesce) Run(t *testing.T, ctx context.Context) {
	o.sched.WaitUntilRunning(t, ctx)

	o.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
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

	openAndReport := func(host *schedulerv1pb.ActorHost) schedulerv1pb.Scheduler_ReportActorTypesClient {
		var stream schedulerv1pb.Scheduler_ReportActorTypesClient
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			stream, err = o.sched.Client(t, ctx).ReportActorTypes(ctx)
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

	hostsOf := func(order *schedulerv1pb.PlacementOrder) []string {
		table, ok := order.GetTables().GetEntries()["typeA"]
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

	host := func(n string) *schedulerv1pb.ActorHost {
		return &schedulerv1pb.ActorHost{
			Address:    "127.0.0.1:4000" + n,
			AppId:      "app-" + n,
			Namespace:  "default",
			ActorTypes: []string{"typeA"},
		}
	}

	streamA := openAndReport(host("1"))
	streamB := openAndReport(host("2"))

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
	ackOrdersUntil(streamA, func(order *schedulerv1pb.PlacementOrder) bool {
		return order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE &&
			len(hostsOf(order)) == 2
	})

	// Two hosts join back to back within the window: the observer's next
	// table for the type carries both at once.
	streamC := openAndReport(host("3"))
	streamD := openAndReport(host("4"))
	for _, s := range []schedulerv1pb.Scheduler_ReportActorTypesClient{streamC, streamD} {
		errS := make(chan error, 1)
		go func() {
			for {
				order, err := s.Recv()
				if err != nil {
					errS <- err
					return
				}
				if err = sendAck(s, order); err != nil {
					errS <- err
					return
				}
			}
		}()
	}

	update := ackOrdersUntil(streamA, func(order *schedulerv1pb.PlacementOrder) bool {
		return order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE &&
			len(hostsOf(order)) > 2
	})
	assert.Len(t, hostsOf(update), 4,
		"churn within the coalesce window must land in one round, not one host at a time")
}
