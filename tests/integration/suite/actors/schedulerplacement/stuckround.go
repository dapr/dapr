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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(stuckround))
}

// stuckround holds one actor type's dissemination round open with a silent
// member: a sidecar joining with another type still becomes ready and
// serves it.
type stuckround struct {
	sched *scheduler.Scheduler
	daprd *daprd.Daprd
	srv   *prochttp.HTTP

	invoked atomic.Int64
}

func (s *stuckround) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["freshtype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/freshtype/", func(w http.ResponseWriter, r *http.Request) {
		s.invoked.Add(1)
	})
	s.srv = prochttp.New(t, prochttp.WithHandler(handler))

	// The dissemination timeout is long so the held round cannot be
	// released by an eviction while the joiner is asserted.
	s.sched = scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithPlacementDisseminateTimeout(time.Minute),
	)
	s.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(s.srv.Port()),
		daprd.WithScheduler(s.sched),
	)

	return []framework.Option{
		framework.WithProcesses(s.sched, s.srv),
	}
}

func (s *stuckround) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)

	// A capable jobs stream opens the gate so the scheduler elects and
	// serves its placement leader.
	s.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})
	sclient := s.sched.Client(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err := sclient.WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return
		}
		var leader bool
		for _, host := range resp.GetHosts() {
			leader = leader || host.GetLeader()
		}
		assert.True(c, leader)
	}, time.Second*20, time.Millisecond*50)

	// The fake host reports t2type and never acks its round, holding it
	// open.
	fake := newSchedulerPlacementStream(t, ctx, s.sched, "t2type")
	fake.withholdAcks.Store(true)
	fake.awaitOrder(t, schedulerv1pb.Operation_OPERATION_LOCK, time.Second*20)

	// A sidecar hosting another type joins while the round is held: it
	// becomes ready and serves its actors.
	s.daprd.Run(t, ctx)
	t.Cleanup(func() { s.daprd.Cleanup(t) })
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "freshtype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*10)
	assert.Positive(t, s.invoked.Load())
}
