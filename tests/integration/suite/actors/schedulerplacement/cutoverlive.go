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
	"strings"
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
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(cutoverlive))
}

// cutoverlive asserts a sidecar adopts scheduler placement without a restart
// when the placement service stands down: the drain already halted every
// actor, so it re-resolves from the same clean slate a restart would give.
type cutoverlive struct {
	sched *scheduler.Scheduler
	place *placement.Placement
	daprd *daprd.Daprd

	invoked atomic.Int64
}

func (c *cutoverlive) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {
		c.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	c.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	c.place = placement.New(t,
		placement.WithSchedulerAddresses(c.sched.Address()),
		placement.WithDisseminateTimeout(time.Second*5),
	)
	c.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(c.sched),
		daprd.WithPlacementAddresses(c.place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(c.sched, srv, c.place, c.daprd),
	}
}

func (c *cutoverlive) Run(t *testing.T, ctx context.Context) {
	c.sched.WaitUntilRunning(t, ctx)

	// An old sidecar's jobs stream holds the capability gate bc omitting the SupportsSchedulerPlacement field.
	oldCtx, oldCancel := context.WithCancel(ctx)
	t.Cleanup(oldCancel)
	c.sched.WatchJobsSuccess(t, oldCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "old-sidecar",
		Namespace: "default",
	})

	c.place.WaitUntilRunning(t, ctx)
	c.daprd.WaitUntilRunning(t, ctx)

	// Actors work through the placement service while the gate holds.
	gclient := c.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(a *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(a, err)
	}, time.Second*20, time.Millisecond*10)
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

	// The gate lifts: the placement service drains and stands down, and
	// the sidecar adopts scheduler placement without a restart.
	oldCancel()

	require.EventuallyWithT(t, func(a *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(a, err)
	}, time.Second*30, time.Millisecond*50)
	assert.Greater(t, c.invoked.Load(), invokedBefore)

	meta, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, "placement: connected", meta.GetActorRuntime().GetPlacement())

	// The scheduler, not the placement service, holds the sidecar's
	// placement stream.
	require.EventuallyWithT(t, func(a *assert.CollectT) {
		var streams float64
		for k, v := range c.sched.Metrics(a, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(a, streams, float64(1))
	}, time.Second*10, time.Millisecond*50)
}
