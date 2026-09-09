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
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disseminate))
}

// schedulerPlacementStream is a raw ReportActorTypes stream used as a
// controllable scheduler placement client. It reports as an actor host and a
// background loop acks every placement order, until withholdAcks makes it go
// silent for the rounds covering t2type, its own type. Rounds for other
// types are still acked, as the scheduler may disseminate them to a new
// stream at any time.
type schedulerPlacementStream struct {
	stream       schedulerv1pb.Scheduler_ReportActorTypesClient
	orders       chan *schedulerv1pb.PlacementOrder
	withholdAcks atomic.Bool
	cancel       context.CancelFunc
}

func newSchedulerPlacementStream(t *testing.T, ctx context.Context, sched *scheduler.Scheduler, actorTypes ...string) *schedulerPlacementStream {
	t.Helper()

	sctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	stream, err := sched.Client(t, ctx).ReportActorTypes(sctx)
	require.NoError(t, err)

	p := &schedulerPlacementStream{
		stream: stream,
		orders: make(chan *schedulerv1pb.PlacementOrder, 16),
		cancel: cancel,
	}
	p.report(t, actorTypes...)

	go func() {
		for {
			order, rerr := stream.Recv()
			if rerr != nil {
				t.Logf("scheduler placement stream ended: %v", rerr)
				close(p.orders)
				return
			}
			if !p.withholdAcks.Load() || !slices.Contains(order.GetActorTypes(), "t2type") {
				//nolint:errcheck
				stream.Send(&schedulerv1pb.ReportActorTypesRequest{
					Msg: &schedulerv1pb.ReportActorTypesRequest_Ack{
						Ack: &schedulerv1pb.PlacementOrderAck{
							Operation: order.GetOperation(),
							Seq:       order.GetSeq(),
						},
					},
				})
			}
			select {
			case p.orders <- order:
			case <-sctx.Done():
				return
			}
		}
	}()

	return p
}

func (p *schedulerPlacementStream) report(t *testing.T, actorTypes ...string) {
	t.Helper()
	require.NoError(t, p.stream.Send(&schedulerv1pb.ReportActorTypesRequest{
		Msg: &schedulerv1pb.ReportActorTypesRequest_Report{
			Report: &schedulerv1pb.ActorHost{
				Address:    "127.0.0.1:40001",
				AppId:      "fake",
				Namespace:  "default",
				ActorTypes: actorTypes,
			},
		},
	}))
}

// sendAck acknowledges a specific order, used to release an order whose ack
// the pump withheld.
func (p *schedulerPlacementStream) sendAck(t *testing.T, order *schedulerv1pb.PlacementOrder) {
	t.Helper()
	require.NoError(t, p.stream.Send(&schedulerv1pb.ReportActorTypesRequest{
		Msg: &schedulerv1pb.ReportActorTypesRequest_Ack{
			Ack: &schedulerv1pb.PlacementOrderAck{
				Operation: order.GetOperation(),
				Seq:       order.GetSeq(),
			},
		},
	}))
}

// awaitOrder blocks until an order with the given operation arrives, failing
// the test on timeout. Orders for other operations are consumed and dropped.
func (p *schedulerPlacementStream) awaitOrder(t *testing.T, op schedulerv1pb.Operation, timeout time.Duration) *schedulerv1pb.PlacementOrder {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case order, ok := <-p.orders:
			if !ok {
				require.Fail(t, "scheduler placement stream closed while awaiting order")
				return nil
			}
			if order.GetOperation() == op {
				return order
			}
		case <-deadline:
			require.Failf(t, "timed out", "no %s order within %s", op, timeout)
			return nil
		}
	}
}

// newActorHost starts an app hosting t1type and a daprd for it against the
// given scheduler.
func newActorHost(t *testing.T, sched *scheduler.Scheduler, invoked *atomic.Int64) (*prochttp.HTTP, *daprd.Daprd) {
	t.Helper()
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["t1type"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/t1type/", func(w http.ResponseWriter, r *http.Request) {
		invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	d := daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(sched),
	)
	return srv, d
}

// disseminate covers the scheduler placement dissemination controls on
// their own topologies: --placement-disseminate-timeout evicting a silent
// stream, and --placement-disseminate-coalesce-window folding churn into
// fewer rounds.
type disseminate struct {
	timeoutDaprd  *daprd.Daprd
	timeoutSched  *scheduler.Scheduler
	coalesceDaprd *daprd.Daprd
	coalesceSched *scheduler.Scheduler

	timeoutInvoked  atomic.Int64
	coalesceInvoked atomic.Int64
}

func (d *disseminate) Setup(t *testing.T) []framework.Option {
	d.timeoutSched = scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithPlacementDisseminateTimeout(time.Second*2),
	)
	timeoutSrv, timeoutDaprd := newActorHost(t, d.timeoutSched, &d.timeoutInvoked)
	d.timeoutDaprd = timeoutDaprd

	d.coalesceSched = scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithPlacementDisseminateCoalesceWindow(time.Second*3),
	)
	coalesceSrv, coalesceDaprd := newActorHost(t, d.coalesceSched, &d.coalesceInvoked)
	d.coalesceDaprd = coalesceDaprd

	return []framework.Option{
		framework.WithProcesses(d.timeoutSched, timeoutSrv, d.timeoutDaprd,
			d.coalesceSched, coalesceSrv, d.coalesceDaprd),
	}
}

func (d *disseminate) Run(t *testing.T, ctx context.Context) {
	t.Run("timeout", d.runTimeout(ctx))
	t.Run("coalesce window", d.runCoalesce(ctx))
}

func (d *disseminate) runTimeout(ctx context.Context) func(*testing.T) {
	return func(t *testing.T) {
		d.timeoutRun(t, ctx)
	}
}

func (d *disseminate) runCoalesce(ctx context.Context) func(*testing.T) {
	return func(t *testing.T) {
		d.coalesceRun(t, ctx)
	}
}

// timeoutRun tests --placement-disseminate-timeout: a stream which stops
// acking scheduler placement orders is evicted when its round times out, the
// round completes for the remaining members, and the surviving host keeps
// serving.
func (d *disseminate) timeoutRun(t *testing.T, ctx context.Context) {
	sched, dd := d.timeoutSched, d.timeoutDaprd
	sched.WaitUntilRunning(t, ctx)
	dd.WaitUntilRunning(t, ctx)

	gclient := dd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "t1type", ActorId: "a1", Method: "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)

	// The fake joins, acks its snapshot, then reports a new type and goes
	// silent mid-round.
	fake := newSchedulerPlacementStream(t, ctx, sched)
	fake.awaitOrder(t, schedulerv1pb.Operation_OPERATION_UNLOCK, time.Second*10)

	fake.withholdAcks.Store(true)
	fake.report(t, "t2type")
	fake.awaitOrder(t, schedulerv1pb.Operation_OPERATION_LOCK, time.Second*10)

	// The scheduler evicts the silent stream when the round times out: the
	// fake's stream is closed by the server.
	start := time.Now()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		select {
		case _, ok := <-fake.orders:
			assert.False(c, ok, "expected the stream to be closed")
		default:
			assert.Fail(c, "stream not closed yet")
		}
	}, time.Second*20, time.Millisecond*50)
	assert.Less(t, time.Since(start), time.Second*15)

	// The surviving host is unaffected before, during and after eviction.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		ictx, cancel := context.WithTimeout(ctx, time.Second*2)
		defer cancel()
		_, err := gclient.InvokeActor(ictx, &rtv1.InvokeActorRequest{
			ActorType: "t1type", ActorId: "a1", Method: "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*50)
}

// coalesceRun tests --placement-disseminate-coalesce-window: churn during
// an in-flight round folds into one follow-up round after the window, while
// the first change of a quiet period still disseminates immediately.
func (d *disseminate) coalesceRun(t *testing.T, ctx context.Context) {
	sched, dd := d.coalesceSched, d.coalesceDaprd
	sched.WaitUntilRunning(t, ctx)
	dd.WaitUntilRunning(t, ctx)

	gclient := dd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "t1type", ActorId: "a1", Method: "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)

	// The fake joins with no types and acks everything.
	fake := newSchedulerPlacementStream(t, ctx, sched)
	fake.awaitOrder(t, schedulerv1pb.Operation_OPERATION_UNLOCK, time.Second*10)

	// The fake reports t2type and holds the resulting round open by
	// withholding its LOCK ack.
	fake.withholdAcks.Store(true)
	fake.report(t, "t2type")
	lockOrder := fake.awaitOrder(t, schedulerv1pb.Operation_OPERATION_LOCK, time.Second*10)

	// Churn during the round: t2type changes again (the fake drops it). The
	// type is locked by the in-flight round, so the change stays pending.
	fake.report(t)

	// Release the held round. On its completion the pending t2type change is
	// not disseminated immediately: the coalesce window is armed.
	fake.withholdAcks.Store(false)
	fake.sendAck(t, lockOrder)
	fake.awaitOrder(t, schedulerv1pb.Operation_OPERATION_UNLOCK, time.Second*10)
	completed := time.Now()

	// The batched round for the pending change arrives only after the
	// window.
	fake.awaitOrder(t, schedulerv1pb.Operation_OPERATION_LOCK, time.Second*20)
	elapsed := time.Since(completed)
	assert.GreaterOrEqual(t, elapsed, time.Second*2,
		"the coalesced round started before the 3s window elapsed")

	// The batched round completes normally.
	fake.awaitOrder(t, schedulerv1pb.Operation_OPERATION_UNLOCK, time.Second*20)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		ictx, cancel := context.WithTimeout(ctx, time.Second*2)
		defer cancel()
		_, err := gclient.InvokeActor(ictx, &rtv1.InvokeActorRequest{
			ActorType: "t1type", ActorId: "a1", Method: "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*50)
}
