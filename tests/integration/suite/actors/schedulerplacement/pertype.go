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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(pertype))
}

// pertype proves dissemination is scoped to the actor types whose membership
// changed: a fake sidecar holds a T2 round in flight by withholding its LOCK
// ack, T2 lookups block, and T1 actors keep serving throughout.
type pertype struct {
	daprd *daprd.Daprd
	sched *scheduler.Scheduler

	invokedT1 atomic.Int64
}

func (p *pertype) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["t1type"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/t1type/", func(w http.ResponseWriter, r *http.Request) {
		p.invokedT1.Add(1)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	// A wide disseminate timeout keeps the stuck [t2type] round in flight
	// long enough to assert against without racing the eviction.
	p.sched = scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithPlacementDisseminateTimeout(time.Second*30),
	)
	p.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(p.sched),
	)

	return []framework.Option{
		framework.WithProcesses(p.sched, srv, p.daprd),
	}
}

func (p *pertype) Run(t *testing.T, ctx context.Context) {
	p.sched.WaitUntilRunning(t, ctx)
	p.daprd.WaitUntilRunning(t, ctx)

	gclient := p.daprd.GRPCClient(t, ctx)

	// T1 actors are up.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "t1type", ActorId: "a1", Method: "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)

	// A fake sidecar joins the placement stream hosting nothing, acking
	// everything it receives so it becomes an established round member.
	fakeCtx, fakeCancel := context.WithCancel(ctx)
	t.Cleanup(fakeCancel)
	stream, err := p.sched.Client(t, ctx).ReportActorTypes(fakeCtx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&schedulerv1pb.ReportActorTypesRequest{
		Msg: &schedulerv1pb.ReportActorTypesRequest_Report{
			Report: &schedulerv1pb.ActorHost{
				Address:   "127.0.0.1:40001",
				AppId:     "fake",
				Namespace: "default",
			},
		},
	}),
	)

	// ack echoes an order; withholdAcks stops the pump mid-round.
	var withholdAcks atomic.Bool
	orders := make(chan *schedulerv1pb.PlacementOrder, 16)
	go func() {
		for {
			order, rerr := stream.Recv()
			if rerr != nil {
				close(orders)
				return
			}
			if !withholdAcks.Load() {
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
			case orders <- order:
			case <-fakeCtx.Done():
				return
			}
		}
	}()

	// The join snapshot arrives and is acked: LOCK, UPDATE, UNLOCK.
	for _, exp := range []schedulerv1pb.Operation{
		schedulerv1pb.Operation_OPERATION_LOCK,
		schedulerv1pb.Operation_OPERATION_UPDATE,
		schedulerv1pb.Operation_OPERATION_UNLOCK,
	} {
		select {
		case order := <-orders:
			require.Equal(t, exp, order.GetOperation())
		case <-time.After(time.Second * 10):
			require.Fail(t, "timed out waiting for join snapshot order")
		}
	}

	// The fake now reports hosting t2type and goes silent. The [t2type]
	// round LOCKs every stream in the namespace and cannot advance past the
	// LOCK phase without the fake's ack.
	withholdAcks.Store(true)
	require.NoError(t, stream.Send(&schedulerv1pb.ReportActorTypesRequest{
		Msg: &schedulerv1pb.ReportActorTypesRequest_Report{
			Report: &schedulerv1pb.ActorHost{
				Address:    "127.0.0.1:40001",
				AppId:      "fake",
				Namespace:  "default",
				ActorTypes: []string{"t2type"},
			},
		},
	}),
	)

	// Receiving the round's LOCK proves the round is in flight, and the
	// withheld ack holds it open until the disseminate timeout.
	func() {
		for {
			select {
			case order := <-orders:
				if order.GetOperation() == schedulerv1pb.Operation_OPERATION_LOCK &&
					slices.Contains(order.GetActorTypes(), "t2type") {
					return
				}
			case <-time.After(time.Second * 15):
				require.Fail(t, "timed out waiting for the [t2type] round LOCK")
			}
		}
	}()

	// While t2type is mid-round and locked, t1type keeps serving.
	invokedBefore := p.invokedT1.Load()
	for range 20 {
		ictx, cancel := context.WithTimeout(ctx, time.Second*2)
		_, ierr := gclient.InvokeActor(ictx, &rtv1.InvokeActorRequest{
			ActorType: "t1type", ActorId: "a1", Method: "foo",
		})
		cancel()
		require.NoError(t, ierr, "t1type must keep serving while t2type is mid-dissemination")
	}
	assert.GreaterOrEqual(t, p.invokedT1.Load(), invokedBefore+20)

	// Control: t2type blocks during its own round, paying for its own
	// rebalance, while t1type above paid nothing.
	lctx, cancel := context.WithTimeout(ctx, time.Millisecond*600)
	t.Cleanup(cancel)
	_, ierr := gclient.InvokeActor(lctx, &rtv1.InvokeActorRequest{
		ActorType: "t2type", ActorId: "b1", Method: "foo",
	})
	require.Equal(t, codes.DeadlineExceeded, status.Code(ierr))

	// The fake disconnects - the scheduler evicts it and completes the round
	// for the remaining members (covered at unit level by
	// TestStreamCloseAdvancesRound). t1type keeps serving throughout.
	fakeCancel()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		ictx, icancel := context.WithTimeout(ctx, time.Second*2)
		defer icancel()
		_, uerr := gclient.InvokeActor(ictx, &rtv1.InvokeActorRequest{
			ActorType: "t1type", ActorId: "a1", Method: "foo",
		})
		assert.NoError(c, uerr)
	}, time.Second*20, time.Millisecond*50)
}
