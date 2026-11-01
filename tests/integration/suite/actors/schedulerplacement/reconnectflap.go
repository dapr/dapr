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
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reconnectflap))
}

// reconnectflap restarts the only sidecar after the cutover: its reported
// placement address count drops to zero and back, and the pending
// re-detection must not withdraw the standing advertisement, which would
// close every placement stream.
type reconnectflap struct {
	sched *scheduler.Scheduler
	place *placement.Placement
	srv   *prochttp.HTTP
}

func (r *reconnectflap) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	r.place = placement.New(t)

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
		framework.WithProcesses(r.sched, r.place, r.srv),
	}
}

func (r *reconnectflap) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)

	newDaprd := func() *daprd.Daprd {
		d := daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(r.srv.Port()),
			daprd.WithScheduler(r.sched),
			daprd.WithPlacementAddresses(r.place.Address()),
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
		}, time.Second*20, time.Millisecond*10)
	}

	first := newDaprd()
	invoke(first)

	// The placement service is removed and the sidecar adopts the scheduler.
	r.place.Cleanup(t)
	invoke(first)

	sclient := r.sched.Client(t, ctx)
	leader := func() (leader, ok bool) {
		stream, err := sclient.WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return false, false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return false, false
		}
		for _, host := range resp.GetHosts() {
			if host.GetLeader() {
				return true, true
			}
		}
		return false, true
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		l, ok := leader()
		assert.True(c, ok && l)
	}, time.Second*20, time.Millisecond*10)

	// The sampler watches the advertisement across the restart.
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
			if l, ok := leader(); ok && !l {
				sawWithdrawn.Store(true)
			}
		}
	}()

	first.Cleanup(t)
	second := newDaprd()
	invoke(second)

	samplerCancel()
	<-samplerDone
	assert.False(t, sawWithdrawn.Load(),
		"a sidecar restart must not withdraw the placement advertisement")
}
