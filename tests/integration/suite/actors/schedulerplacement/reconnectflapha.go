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
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reconnectflapha))
}

// reconnectflapha restarts the only sidecar under a three scheduler cluster:
// no scheduler may withdraw the leader or mask the capability bit across the
// restart.
type reconnectflapha struct {
	cluster   *cluster.Cluster
	srv       *prochttp.HTTP
	placeAddr string
}

func (r *reconnectflapha) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.cluster = cluster.New(t,
		cluster.WithCount(3),
		cluster.WithSchedulerOptions(scheduler.WithPlacementEnabled(true)),
	)

	// The reserved port is never freed, so the reported placement address
	// is never sighted as a placement service.
	r.placeAddr = fmt.Sprintf("127.0.0.1:%d", ports.Reserve(t, 1).Port(t))

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, req *http.Request) {})
	r.srv = prochttp.New(t, prochttp.WithHandler(handler))

	return []framework.Option{
		framework.WithProcesses(r.cluster, r.srv),
	}
}

func (r *reconnectflapha) Run(t *testing.T, ctx context.Context) {
	r.cluster.WaitUntilRunning(t, ctx)

	newDaprd := func() *daprd.Daprd {
		d := daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(r.srv.Port()),
			daprd.WithSchedulerAddresses(r.cluster.Addresses()...),
			daprd.WithPlacementAddresses(r.placeAddr),
		)
		d.Run(t, ctx)
		t.Cleanup(func() { d.Cleanup(t) })
		d.WaitUntilRunning(t, ctx)
		return d
	}
	invoke := func(d *daprd.Daprd) {
		client := d.GRPCClient(t, ctx)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "myactortype",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)
		}, time.Second*15, time.Millisecond*10)
	}

	first := newDaprd()
	invoke(first)

	hosts := func(n int) (leader, capable, ok bool) {
		stream, err := r.cluster.ClientN(t, ctx, n).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return false, false, false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return false, false, false
		}
		for _, host := range resp.GetHosts() {
			leader = leader || host.GetLeader()
			capable = capable || host.GetSchedulerPlacementEnabled()
		}
		return leader, capable, true
	}
	for n := range 3 {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			leader, capable, ok := hosts(n)
			assert.True(c, ok && leader && capable)
		}, time.Second*15, time.Millisecond*10)
	}

	// watch every scheduler's broadcast across the restart.
	var sawWithdrawn atomic.Bool
	samplerCtx, samplerCancel := context.WithCancel(ctx)
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		for {
			select {
			case <-samplerCtx.Done():
				return
			case <-time.After(time.Millisecond * 50):
			}
			for n := range 3 {
				if leader, capable, ok := hosts(n); ok && (!leader || !capable) {
					sawWithdrawn.Store(true)
				}
			}
		}
	}()

	first.Cleanup(t)
	second := newDaprd()
	invoke(second)

	samplerCancel()
	<-samplerDone
	assert.False(t, sawWithdrawn.Load(),
		"no scheduler may withdraw the leader or mask the capability bit across a sidecar restart")
}
