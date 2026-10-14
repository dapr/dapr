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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(cutoverlive))
}

// cutoverlive asserts a sidecar adopts scheduler placement without a restart
// when the placement service is removed: the scheduler withholds the
// placement leader while a placement service is present, and advertises once
// it is gone.
type cutoverlive struct {
	sched  *scheduler.Scheduler
	place  *placement.Placement
	daprds [3]*daprd.Daprd

	invoked atomic.Int64
}

func (c *cutoverlive) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	c.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*5),
	)

	procs := make([]process.Interface, 0, 2+2*len(c.daprds))
	procs = append(procs, c.sched, c.place)
	for i := range c.daprds {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				c.invoked.Add(1)
			}
		})
		srv := prochttp.New(t, prochttp.WithHandler(handler))
		c.daprds[i] = daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(srv.Port()),
			daprd.WithScheduler(c.sched),
			daprd.WithPlacementAddresses(c.place.Address()),
		)
		procs = append(procs, srv, c.daprds[i])
	}

	return []framework.Option{
		framework.WithProcesses(procs...),
	}
}

func (c *cutoverlive) Run(t *testing.T, ctx context.Context) {
	c.sched.WaitUntilRunning(t, ctx)

	// An old sidecar's jobs stream holds the capability gate bc omitting the
	// SupportsSchedulerPlacement field.
	oldCtx, oldCancel := context.WithCancel(ctx)
	t.Cleanup(oldCancel)
	c.sched.WatchJobsSuccess(t, oldCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "old-sidecar",
		Namespace: "default",
	})

	c.place.WaitUntilRunning(t, ctx)
	for _, d := range c.daprds {
		d.WaitUntilRunning(t, ctx)
	}

	const instances = 30
	ids := make([]string, instances)
	for i := range ids {
		ids[i] = fmt.Sprintf("cutover-%d", i)
	}

	// Actors work through the placement service while the gate holds.
	gclient := c.daprds[0].GRPCClient(t, ctx)
	invokeAll := func() {
		for _, id := range ids {
			require.EventuallyWithT(t, func(a *assert.CollectT) {
				_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
					ActorType: "myactortype",
					ActorId:   id,
					Method:    "foo",
				})
				assert.NoError(a, err)
			}, time.Second*20, time.Millisecond*10)
		}
	}
	invokeAll()
	invokedBefore := c.invoked.Load()

	// The scheduler holds no placement stream while the gate holds: the
	// actors above were served by the placement service.
	var streamsBefore float64
	for k, v := range c.sched.Metrics(t, ctx).All() {
		if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
			streamsBefore += v
		}
	}
	assert.Zero(t, streamsBefore)

	// The gate lifts, but the placement service is still present: the
	// scheduler keeps withholding the placement leader and the sidecar stays
	// on the placement service.
	oldCancel()

	time.Sleep(time.Second * 3)
	var streamsHeld float64
	for k, v := range c.sched.Metrics(t, ctx).All() {
		if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
			streamsHeld += v
		}
	}
	assert.Zero(t, streamsHeld)

	_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   ids[0],
		Method:    "foo",
	})
	require.NoError(t, err)

	// The placement service is removed: its absence hands the placement
	// authority to the scheduler, and the sidecar adopts it without a
	// restart.
	c.place.Cleanup(t)

	invokeAll()
	assert.Greater(t, c.invoked.Load(), invokedBefore)

	meta, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, "placement: connected", meta.GetActorRuntime().GetPlacement())

	// The scheduler, not the placement service, holds every sidecar's
	// placement stream.
	require.EventuallyWithT(t, func(a *assert.CollectT) {
		var streams float64
		for k, v := range c.sched.Metrics(a, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(a, streams, float64(3))
	}, time.Second*10, time.Millisecond*10)

	// A sidecar restarting after the cutover drops and re-reports its
	// configured placement addresses: presence must not flap the authority
	// back.
	c.daprds[2].Cleanup(t)
	restarted := daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithScheduler(c.sched),
		daprd.WithPlacementAddresses(c.place.Address()),
	)
	restarted.Run(t, ctx)
	t.Cleanup(func() { restarted.Cleanup(t) })
	restarted.WaitUntilRunning(t, ctx)

	require.Never(t, func() bool {
		var l float64
		for k, v := range c.sched.Metrics(t, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_leader") {
				l += v
			}
		}
		return l == 0
	}, time.Second*5, time.Millisecond*10,
		"a sidecar restart must not flap the authority back to placement")
	invokeAll()
}
