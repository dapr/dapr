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
	"net/http"
	"strings"
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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(probecancel))
}

type probecancel struct {
	sched *scheduler.Scheduler
	back  *scheduler.Scheduler
	daprd *daprd.Daprd

	place   *procgrpc.GRPC
	reports atomic.Int64
	probes  chan struct{}

	invoked atomic.Int64
}

func (c *probecancel) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	c.probes = make(chan struct{}, 1)
	// Answers Unimplemented so the scheduler's probe finds no placement service,
	// and counts every report a sidecar sends to it. A sidecar reports before
	// anything else; a probe closes its send side without a report, and is held
	// for a moment so the scheduler can be stopped while it is in flight, within
	// the probe's own deadline.
	c.place = procgrpc.New(t, procgrpc.WithServerOption(func(*testing.T, context.Context) grpc.ServerOption {
		return grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
			if stream.RecvMsg(new(placementv1pb.Host)) == nil {
				c.reports.Add(1)
				return status.Error(codes.Unimplemented, "not a placement service")
			}
			select {
			case c.probes <- struct{}{}:
			default:
			}
			select {
			case <-time.After(time.Second):
			case <-stream.Context().Done():
			}
			return status.Error(codes.Unimplemented, "not a placement service")
		})
	}))

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

	c.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	c.back = scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithID(c.sched.ID()),
		scheduler.WithPort(c.sched.Port()),
		scheduler.WithEtcdClientPort(c.sched.EtcdClientPort()),
		scheduler.WithInitialCluster(c.sched.InitialCluster()),
		scheduler.WithDataDir(c.sched.DataDir()),
	)
	c.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(c.sched),
		daprd.WithPlacementAddresses(c.place.Address(t)),
	)

	return []framework.Option{
		framework.WithProcesses(c.place, c.sched, srv, c.daprd),
	}
}

func (c *probecancel) Run(t *testing.T, ctx context.Context) {
	c.sched.WaitUntilRunning(t, ctx)

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

	invoke()
	assert.Zero(t, c.reports.Load(), "boot: sidecar reported to the placement service")

	// The probe which gated the boot advertisement is done; stop the
	// scheduler inside the next periodic one.
	select {
	case <-c.probes:
	default:
	}
	select {
	case <-c.probes:
	case <-time.After(time.Second * 15):
		require.Fail(t, "the scheduler never probed the placement address again")
	}
	c.sched.Cleanup(t)

	require.EventuallyWithT(t, func(col *assert.CollectT) {
		meta, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		if !assert.NoError(col, err) {
			return
		}
		assert.Equal(col, "placement: disconnected", meta.GetActorRuntime().GetPlacement())
	}, time.Second*20, time.Millisecond*10)
	// Give a sidecar which read the shutdown broadcast as "no scheduler
	// placement" time to act on it before the scheduler is back.
	time.Sleep(time.Second * 2)
	assert.Zero(t, c.reports.Load(), "shutdown: sidecar reported to the placement service")

	c.back.Run(t, ctx)
	t.Cleanup(func() { c.back.Cleanup(t) })
	c.back.WaitUntilRunning(t, ctx)

	// A fresh process only counts its own streams, so this means the sidecar
	// re-adopted the scheduler.
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		var streams float64
		for k, v := range c.back.Metrics(col, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(col, streams, float64(1))
	}, time.Second*20, time.Millisecond*10)
	invoke()
	assert.Zero(t, c.reports.Load(), "cold start: sidecar reported to the placement service")
}
