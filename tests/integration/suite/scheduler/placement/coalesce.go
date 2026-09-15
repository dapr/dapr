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

// coalesce asserts the coalesce window batches membership churn: the
// window arms when a round completes with churn accumulated behind it, and
// a host joining inside the armed window merges into the one follow-up
// round. An unbatched scheduler disseminates that follow-up immediately,
// which the observer sees as an extra 4 host update before the 5 host one.
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

	host := func(n string) *schedulerv1pb.ActorHost {
		return &schedulerv1pb.ActorHost{
			Address:    "127.0.0.1:4000" + n,
			AppId:      "app-" + n,
			Namespace:  "default",
			ActorTypes: []string{"typeA"},
		}
	}

	streamA := openAndReport(host("1"))

	// The observer acks every order and forwards its typeA updates in
	// order.
	updatesA := make(chan *schedulerv1pb.PlacementOrder, 16)
	errA := make(chan error, 1)
	go func() {
		for {
			order, err := streamA.Recv()
			if err != nil {
				errA <- err
				return
			}
			if err = sendAck(streamA, order); err != nil {
				errA <- err
				return
			}
			if order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE {
				if _, ok := order.GetTables().GetEntries()["typeA"]; ok {
					updatesA <- order
				}
			}
		}
	}()

	ackAll := func(stream schedulerv1pb.Scheduler_ReportActorTypesClient) {
		go func() {
			for {
				order, err := stream.Recv()
				if err != nil {
					return
				}
				if sendAck(stream, order) != nil {
					return
				}
			}
		}()
	}
	streamB := openAndReport(host("2"))
	ackAll(streamB)

	nextUpdate := func() *schedulerv1pb.PlacementOrder {
		select {
		case update := <-updatesA:
			return update
		case err := <-errA:
			require.Fail(t, "the observer stream failed", err)
		case <-time.After(time.Second * 20):
			require.Fail(t, "no update arrived")
		}
		return nil
	}

	// Drain the observer's updates until the second host's join round
	// completed, so the held round phase starts from a settled 2 host
	// table.
	update := nextUpdate()
	for len(hostsOf(update)) != 2 {
		update = nextUpdate()
	}

	// The third host withholds its join round's LOCK ack, holding the round
	// in flight while the fourth host joins behind it. The join round's
	// LOCK carries the type where the snapshot's carries none.
	streamC := openAndReport(host("3"))
	release := make(chan struct{})
	go func() {
		for {
			order, err := streamC.Recv()
			if err != nil {
				return
			}
			if order.GetOperation() == schedulerv1pb.Operation_OPERATION_LOCK && len(order.GetActorTypes()) > 0 {
				<-release
			}
			if sendAck(streamC, order) != nil {
				return
			}
		}
	}()

	streamD := openAndReport(host("4"))
	ackAll(streamD)
	// The fourth host's churn lands behind the held round before it is
	// released, so the round completes with churn pending and the window
	// arms.
	time.Sleep(time.Second)
	close(release)

	update = nextUpdate()
	require.Len(t, hostsOf(update), 4, "the held round's update carries the membership at send time")

	// A fifth host joins inside the armed window: its churn merges into the
	// one follow-up round, so the observer's next update carries 5 hosts,
	// never 4 again.
	streamE := openAndReport(host("5"))
	ackAll(streamE)

	update = nextUpdate()
	assert.Len(t, hostsOf(update), 5,
		"churn within the coalesce window must land in one round, not one host at a time")
}
