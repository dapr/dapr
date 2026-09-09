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
	"strconv"
	"sync"
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
	suite.Register(new(churn))
}

// churn opens and abruptly closes many streams concurrently in one
// namespace, cancelling some mid round, while two survivors ack every order:
// the survivors are never cancelled and the tables converge on exactly the
// survivors.
type churn struct {
	sched *scheduler.Scheduler
}

func (h *churn) Setup(t *testing.T) []framework.Option {
	h.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	return []framework.Option{
		framework.WithProcesses(h.sched),
	}
}

func (h *churn) Run(t *testing.T, ctx context.Context) {
	h.sched.WaitUntilRunning(t, ctx)

	h.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
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
			stream, err = h.sched.Client(t, ctx).ReportActorTypes(ctx)
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
		table, ok := order.GetTables().GetEntries()["typeX"]
		if !ok {
			return nil
		}
		addrs := make([]string, 0, len(table.GetHosts()))
		for addr := range table.GetHosts() {
			addrs = append(addrs, addr)
		}
		return addrs
	}

	host := func(addr string) *schedulerv1pb.ActorHost {
		return &schedulerv1pb.ActorHost{
			Address:    addr,
			AppId:      "app-" + addr,
			Namespace:  "default",
			ActorTypes: []string{"typeX"},
		}
	}

	// Two survivors ack every order for the whole test. Their latest table
	// for the type is kept for the final convergence assertion.
	survivors := []string{"127.0.0.1:41001", "127.0.0.1:41002"}
	type survivorState struct {
		updates chan *schedulerv1pb.PlacementOrder
		err     chan error
	}
	states := make([]*survivorState, len(survivors))
	for i, addr := range survivors {
		stream := openAndReport(host(addr))
		state := &survivorState{
			updates: make(chan *schedulerv1pb.PlacementOrder, 1),
			err:     make(chan error, 1),
		}
		states[i] = state
		go func() {
			for {
				order, err := stream.Recv()
				if err != nil {
					state.err <- err
					return
				}
				if err = sendAck(stream, order); err != nil {
					state.err <- err
					return
				}
				if order.GetOperation() != schedulerv1pb.Operation_OPERATION_UPDATE {
					continue
				}
				for {
					select {
					case state.updates <- order:
					default:
						select {
						case <-state.updates:
						default:
						}
						continue
					}
					break
				}
			}
		}()
	}

	// Churners open a stream, ack a couple of orders and vanish without
	// closing cleanly, so some die mid round and mid Send.
	var wg sync.WaitGroup
	for c := range 6 {
		wg.Go(func() {
			for i := range 4 {
				// Bounded: a churner blocked in Recv otherwise waits out the
				// disseminate timeout, stacking iterations toward the suite's
				// per test kill.
				cctx, cancel := context.WithTimeout(ctx, time.Second*2)
				stream, err := h.sched.Client(t, cctx).ReportActorTypes(cctx)
				if err != nil {
					cancel()
					continue
				}
				addr := "127.0.0.1:42" + strconv.Itoa(c) + "0" + strconv.Itoa(i)
				if err = stream.Send(&schedulerv1pb.ReportActorTypesRequest{
					Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: host(addr)},
				}); err != nil {
					cancel()
					continue
				}
				for range 2 {
					order, rerr := stream.Recv()
					if rerr != nil {
						break
					}
					if serr := sendAck(stream, order); serr != nil {
						break
					}
				}
				cancel()
			}
		})
	}
	wg.Wait()

	// The churn settles: the survivors were never cancelled and their
	// tables converge on exactly the survivors.
	deadline := time.After(time.Second * 20)
	for _, state := range states {
		for {
			select {
			case update := <-state.updates:
				hosts := hostsOf(update)
				if len(hosts) == len(survivors) {
					assert.ElementsMatch(t, survivors, hosts)
				} else {
					continue
				}
			case err := <-state.err:
				require.Fail(t, "a survivor stream was cancelled", err)
			case <-deadline:
				require.Fail(t, "the survivors never converged")
			}
			break
		}
	}
}
