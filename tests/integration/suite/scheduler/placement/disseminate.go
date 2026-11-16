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
	suite.Register(new(disseminate))
}

// disseminate drives the 3 phase lock/update/unlock protocol over raw
// ReportActorTypes streams: a host's report lands in the disseminated
// tables, a second host joining rebalances every stream, and the tables
// carry the rendezvous hash algorithm and per type versions.
type disseminate struct {
	sched *scheduler.Scheduler
}

func (d *disseminate) Setup(t *testing.T) []framework.Option {
	d.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	return []framework.Option{
		framework.WithProcesses(d.sched),
	}
}

func (d *disseminate) Run(t *testing.T, ctx context.Context) {
	d.sched.WaitUntilRunning(t, ctx)

	d.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
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

	// openAndReport opens a ReportActorTypes stream and sends the host
	// report, retrying while the leader election settles. A receive
	// surfaces a still settling election as a stream error, where the send
	// alone may not.
	openAndReport := func(host *schedulerv1pb.ActorHost) schedulerv1pb.Scheduler_ReportActorTypesClient {
		var stream schedulerv1pb.Scheduler_ReportActorTypesClient
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var err error
			stream, err = d.sched.Client(t, ctx).ReportActorTypes(ctx)
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

	typeAHosts := func(order *schedulerv1pb.PlacementOrder) []string {
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

	// ackOrdersUntil acknowledges every order until one matches, returning
	// it. Error returning rather than failing, so it can run on a
	// goroutine.
	ackOrdersUntil := func(stream schedulerv1pb.Scheduler_ReportActorTypesClient, match func(*schedulerv1pb.PlacementOrder) bool) (*schedulerv1pb.PlacementOrder, error) {
		for {
			order, err := stream.Recv()
			if err != nil {
				return nil, err
			}
			if err = sendAck(stream, order); err != nil {
				return nil, err
			}
			if match(order) {
				return order, nil
			}
		}
	}

	streamA := openAndReport(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40001",
		AppId:      "app-a",
		Namespace:  "default",
		ActorTypes: []string{"typeA"},
	})

	// The snapshot round delivers this host its own table.
	update, err := ackOrdersUntil(streamA, func(order *schedulerv1pb.PlacementOrder) bool {
		return order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE &&
			len(typeAHosts(order)) > 0
	})
	require.NoError(t, err)
	assert.Equal(t, schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_RENDEZVOUS, update.GetTables().GetHashAlgorithm())
	assert.ElementsMatch(t, []string{"127.0.0.1:40001"}, typeAHosts(update))
	assert.Contains(t, update.GetVersions(), "typeA")
	table := update.GetTables().GetEntries()["typeA"]
	require.NotNil(t, table)
	assert.Equal(t, "app-a", table.GetHosts()["127.0.0.1:40001"].GetAppId())

	// A second host joins the same type: both streams converge on a table
	// with both hosts.
	streamB := openAndReport(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40002",
		AppId:      "app-b",
		Namespace:  "default",
		ActorTypes: []string{"typeA"},
	})

	bothHosts := func(order *schedulerv1pb.PlacementOrder) bool {
		return order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE &&
			len(typeAHosts(order)) == 2
	}
	// The join round only advances once both streams acked each phase, so
	// both streams must ack concurrently.
	errA := make(chan error, 1)
	var updateA *schedulerv1pb.PlacementOrder
	go func() {
		var aerr error
		updateA, aerr = ackOrdersUntil(streamA, bothHosts)
		errA <- aerr
	}()
	updateB, err := ackOrdersUntil(streamB, bothHosts)
	require.NoError(t, err)
	require.NoError(t, <-errA)
	want := []string{"127.0.0.1:40001", "127.0.0.1:40002"}
	assert.ElementsMatch(t, want, typeAHosts(updateA))
	assert.ElementsMatch(t, want, typeAHosts(updateB))
}
