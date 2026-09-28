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

package coldstart

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ha))
}

// ha asserts a sidecar with an unserved placement address never
// falls back to the placement service when all 3 schedulers cold start.
type ha struct {
	schedulers [3]*scheduler.Scheduler
	back       [3]*scheduler.Scheduler
	daprd      *daprd.Daprd

	place   *procgrpc.GRPC
	reports atomic.Int64

	invoked atomic.Int64
}

func (c *ha) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	fp := ports.Reserve(t, 6)
	peer1, peer2, peer3 := fp.Port(t), fp.Port(t), fp.Port(t)
	etcd1, etcd2, etcd3 := fp.Port(t), fp.Port(t), fp.Port(t)
	// Answers Unimplemented so the scheduler's probe finds no placement
	// service, and counts every report a sidecar sends to it.
	c.place = procgrpc.New(t, procgrpc.WithServerOption(func(*testing.T, context.Context) grpc.ServerOption {
		return grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
			if stream.RecvMsg(new(placementv1pb.Host)) == nil {
				c.reports.Add(1)
			}
			return status.Error(codes.Unimplemented, "not a placement service")
		})
	}))

	initialCluster := fmt.Sprintf(
		"scheduler-0=http://127.0.0.1:%d,scheduler-1=http://127.0.0.1:%d,scheduler-2=http://127.0.0.1:%d",
		peer1, peer2, peer3,
	)
	etcdPorts := [3]int{etcd1, etcd2, etcd3}

	for i := range c.schedulers {
		c.schedulers[i] = scheduler.New(t,
			scheduler.WithPlacementEnabled(true),
			scheduler.WithInitialCluster(initialCluster),
			scheduler.WithID("scheduler-"+strconv.Itoa(i)),
			scheduler.WithEtcdClientPort(etcdPorts[i]),
		)
		c.back[i] = scheduler.New(t,
			scheduler.WithPlacementEnabled(true),
			scheduler.WithInitialCluster(initialCluster),
			scheduler.WithID(c.schedulers[i].ID()),
			scheduler.WithPort(c.schedulers[i].Port()),
			scheduler.WithEtcdClientPort(etcdPorts[i]),
			scheduler.WithDataDir(c.schedulers[i].DataDir()),
		)
	}

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
		c.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	c.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(
			c.schedulers[0].Address(),
			c.schedulers[1].Address(),
			c.schedulers[2].Address(),
		),
		daprd.WithPlacementAddresses(c.place.Address(t)),
	)

	return []framework.Option{
		framework.WithProcesses(fp, c.place,
			c.schedulers[0], c.schedulers[1], c.schedulers[2],
			srv, c.daprd,
		),
	}
}

func (c *ha) Run(t *testing.T, ctx context.Context) {
	for _, sched := range c.schedulers {
		sched.WaitUntilRunning(t, ctx)
	}

	gclient := c.daprd.GRPCClient(t, ctx)
	invoke := func() {
		t.Helper()
		before := c.invoked.Load()
		require.EventuallyWithT(t, func(col *assert.CollectT) {
			if !assert.Zero(col, c.reports.Load(), "sidecar reported to the placement service") {
				return
			}
			ictx, cancel := context.WithTimeout(ctx, time.Second*2)
			defer cancel()
			_, err := gclient.InvokeActor(ictx, &rtv1.InvokeActorRequest{
				ActorType: "myactortype",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(col, err)
		}, time.Second*30, time.Millisecond*10)
		assert.Greater(t, c.invoked.Load(), before)
	}

	// Fresh processes only count their own streams, so this means the sidecar
	// re-adopted them.
	waitStreams := func(scheds ...*scheduler.Scheduler) {
		t.Helper()
		require.EventuallyWithT(t, func(col *assert.CollectT) {
			var streams float64
			for _, sched := range scheds {
				for k, v := range sched.Metrics(col, ctx).All() {
					if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
						streams += v
					}
				}
			}
			assert.GreaterOrEqual(col, streams, float64(1))
		}, time.Second*45, time.Millisecond*10)
	}

	assertNoFallback := func(phase string) {
		t.Helper()
		assert.Zero(t, c.reports.Load(), "%s: sidecar reported to the placement service", phase)
	}

	invoke()
	assertNoFallback("boot")

	// Stopped together so each still has quorum to revoke its cron lease.
	var wg sync.WaitGroup
	for _, sched := range c.schedulers {
		wg.Go(func() { sched.Cleanup(t) })
	}
	wg.Wait()
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		meta, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		if !assert.NoError(col, err) {
			return
		}
		assert.Equal(col, "placement: disconnected", meta.GetActorRuntime().GetPlacement())
	}, time.Second*20, time.Millisecond*10)

	for _, sched := range c.back {
		sched.Run(t, ctx)
		t.Cleanup(func() { sched.Cleanup(t) })
		time.Sleep(time.Second)
	}
	for _, sched := range c.back {
		sched.WaitUntilRunning(t, ctx)
	}

	waitStreams(c.back[0], c.back[1], c.back[2])
	invoke()
	assertNoFallback("cold start")
}
