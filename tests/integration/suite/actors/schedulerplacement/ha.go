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
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strconv"
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
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ha))
}

// ha tests placement on a 3 replica scheduler cluster: killing the leader
// elects a new one which rebuilds the table from the live streams, and actors
// recover with no sidecar restart and no persisted placement state.
type ha struct {
	schedulers [3]*scheduler.Scheduler
	daprd      *daprd.Daprd

	invoked atomic.Int64
}

func (h *ha) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Cleanup does not work cleanly on windows")
	}

	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	port4, port5, port6 := fp.Port(t), fp.Port(t), fp.Port(t)

	opts := []scheduler.Option{
		scheduler.WithPlacementEnabled(true),
		scheduler.WithInitialCluster(fmt.Sprintf(
			"scheduler-0=http://127.0.0.1:%d,scheduler-1=http://127.0.0.1:%d,scheduler-2=http://127.0.0.1:%d",
			port1, port2, port3),
		),
	}

	h.schedulers[0] = scheduler.New(t, append(opts, scheduler.WithID("scheduler-0"), scheduler.WithEtcdClientPort(port4))...)
	h.schedulers[1] = scheduler.New(t, append(opts, scheduler.WithID("scheduler-1"), scheduler.WithEtcdClientPort(port5))...)
	h.schedulers[2] = scheduler.New(t, append(opts, scheduler.WithID("scheduler-2"), scheduler.WithEtcdClientPort(port6))...)

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
		h.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	h.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithSchedulerAddresses(
			h.schedulers[0].Address(),
			h.schedulers[1].Address(),
			h.schedulers[2].Address(),
		),
	)

	return []framework.Option{
		framework.WithProcesses(fp,
			h.schedulers[0], h.schedulers[1], h.schedulers[2],
			srv, h.daprd,
		),
	}
}

func (h *ha) Run(t *testing.T, ctx context.Context) {
	for _, sched := range h.schedulers {
		sched.WaitUntilRunning(t, ctx)
	}
	h.daprd.WaitUntilRunning(t, ctx)

	// leaderAddress reads the advertised placement leader
	leaderAddress := func(client schedulerv1pb.SchedulerClient) (string, error) {
		stream, err := client.WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return "", err
		}
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return "", err
		}
		for _, host := range resp.GetHosts() {
			if host.GetLeader() {
				return host.GetAddress(), nil
			}
		}
		return "", nil
	}

	// A leader is advertised, and it is the lowest-addressed host.
	scheduler0Client := h.schedulers[0].Client(t, ctx)
	var leaderAddr string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var lerr error
		leaderAddr, lerr = leaderAddress(scheduler0Client)
		if !assert.NoError(c, lerr) {
			return
		}
		assert.NotEmpty(c, leaderAddr)
	}, time.Second*30, time.Millisecond*50)

	lowest := ""
	for _, sched := range h.schedulers {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(sched.Port()))
		if lowest == "" || addr < lowest {
			lowest = addr
		}
	}
	assert.Equal(t, lowest, leaderAddr)

	// Actors work against the leader.
	gclient := h.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype", ActorId: "a1", Method: "foo",
		})
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*10)
	invokedBefore := h.invoked.Load()

	// Take down the scheduler placement leader gracefully so its etcd lease is revoked
	// immediately.
	var survivor *scheduler.Scheduler
	for _, sched := range h.schedulers {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(sched.Port()))
		if addr == leaderAddr {
			sched.Cleanup(t)
		} else if survivor == nil {
			survivor = sched
		}
	}
	require.NotNil(t, survivor)

	// Actors recover on a new leader without a sidecar restart: the
	// next-lowest capable host advertises itself and the table is rebuilt
	// entirely from the sidecar's re-established stream.
	survivorClient := survivor.Client(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		newLeader, lerr := leaderAddress(survivorClient)
		if !assert.NoError(c, lerr) {
			return
		}
		assert.NotEmpty(c, newLeader)
		assert.NotEqual(c, leaderAddr, newLeader)

		ictx, cancel := context.WithTimeout(ctx, time.Second*2)
		defer cancel()
		_, err := gclient.InvokeActor(ictx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype", ActorId: "a1", Method: "foo",
		})
		assert.NoError(c, err)
	}, time.Second*30, time.Millisecond*100)
	assert.Greater(t, h.invoked.Load(), invokedBefore)
}
