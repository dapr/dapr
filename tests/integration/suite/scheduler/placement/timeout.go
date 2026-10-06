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
	suite.Register(new(timeout))
}

// timeout asserts a host which stops acking placement orders is evicted
// after the disseminate timeout: its stream is closed with DeadlineExceeded,
// the aborted round restarts and the surviving hosts converge.
type timeout struct {
	sched *scheduler.Scheduler
}

func (o *timeout) Setup(t *testing.T) []framework.Option {
	o.sched = scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithPlacementDisseminateTimeout(time.Second*3),
	)
	return []framework.Option{
		framework.WithProcesses(o.sched),
	}
}

func (o *timeout) Run(t *testing.T, ctx context.Context) {
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

	streamA := openAndReport(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40001",
		AppId:      "app-a",
		Namespace:  "default",
		ActorTypes: []string{"typeA"},
	})

	// The survivor acks every order for the whole test, starting before the
	// non-acker joins: any unacked round would evict it too.
	converged := func(order *schedulerv1pb.PlacementOrder) bool {
		if order.GetOperation() != schedulerv1pb.Operation_OPERATION_UPDATE {
			return false
		}
		hosts := hostsOf(order, "typeA")
		return len(hosts) == 2
	}
	updatesA := make(chan *schedulerv1pb.PlacementOrder, 1)
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
			if converged(order) {
				// Keep the latest matching update: an interleaving where the
				// join round reached its UPDATE before the eviction leaves a
				// stale two host table behind.
				for {
					select {
					case updatesA <- order:
					default:
						select {
						case <-updatesA:
						default:
						}
						continue
					}
					break
				}
			}
		}
	}()

	// The second host stops acking after its first order: the join round
	// cannot complete, so the host is evicted after the disseminate timeout
	// with DeadlineExceeded.
	streamB := openAndReport(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40002",
		AppId:      "app-b",
		Namespace:  "default",
		ActorTypes: []string{"typeA"},
	})

	errB := make(chan error, 1)
	go func() {
		for {
			if _, err := streamB.Recv(); err != nil {
				errB <- err
				return
			}
		}
	}()

	select {
	case err := <-errB:
		assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
		require.ErrorContains(t, err, "dissemination timeout")
	case err := <-errA:
		require.Fail(t, "the acking survivor must not be evicted", err)
	case <-time.After(time.Second * 20):
		require.Fail(t, "the non-acking host was not evicted")
	}

	// The scheduler still disseminates to survivors: a third host joins and
	// both converge without the evicted host.
	streamC := openAndReport(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40003",
		AppId:      "app-c",
		Namespace:  "default",
		ActorTypes: []string{"typeA"},
	})

	errC := make(chan error, 1)
	var updateC *schedulerv1pb.PlacementOrder
	go func() {
		for {
			order, err := streamC.Recv()
			if err != nil {
				errC <- err
				return
			}
			if err = sendAck(streamC, order); err != nil {
				errC <- err
				return
			}
			if converged(order) {
				updateC = order
				errC <- nil
				return
			}
		}
	}()

	want := []string{"127.0.0.1:40001", "127.0.0.1:40003"}
	require.NoError(t, <-errC)
	assert.ElementsMatch(t, want, hostsOf(updateC, "typeA"))
	deadline := time.After(time.Second * 20)
	for {
		select {
		case updateA := <-updatesA:
			hosts := hostsOf(updateA, "typeA")
			if len(hosts) == 2 && hosts[0] != hosts[1] &&
				(hosts[0] == "127.0.0.1:40003" || hosts[1] == "127.0.0.1:40003") {
				assert.ElementsMatch(t, want, hosts)
				return
			}
		case err := <-errA:
			require.Fail(t, "the acking survivor must not be evicted", err)
		case <-deadline:
			require.Fail(t, "the survivor never converged")
		}
	}
}
