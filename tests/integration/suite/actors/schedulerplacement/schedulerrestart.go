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
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(schedulerrestart))
}

// schedulerrestart kills the scheduler serving placement and brings it back
// on the same address: the sidecar reconnects on its own and actors work
// again with no sidecar restart.
type schedulerrestart struct {
	sched     *scheduler.Scheduler
	schedBack *scheduler.Scheduler
	daprd     *daprd.Daprd

	invoked atomic.Int64
}

func (s *schedulerrestart) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(w http.ResponseWriter, r *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(w http.ResponseWriter, r *http.Request) {
		s.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	s.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	s.schedBack = scheduler.New(t,
		scheduler.WithPlacementEnabled(true),
		scheduler.WithID(s.sched.ID()),
		scheduler.WithPort(s.sched.Port()),
		scheduler.WithEtcdClientPort(s.sched.EtcdClientPort()),
		scheduler.WithInitialCluster(s.sched.InitialCluster()),
		scheduler.WithDataDir(s.sched.DataDir()),
	)
	s.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(s.sched),
	)

	return []framework.Option{
		framework.WithProcesses(s.sched, srv, s.daprd),
	}
}

func (s *schedulerrestart) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	gclient := s.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)
	invokedBefore := s.invoked.Load()

	// The scheduler dies: the sidecar reports the placement connection lost.
	s.sched.Kill(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, "placement: disconnected", meta.GetActorRuntime().GetPlacement())
	}, time.Second*20, time.Millisecond*10)

	// The scheduler returns on the same address and data directory.
	s.schedBack.Run(t, ctx)
	t.Cleanup(func() { s.schedBack.Cleanup(t) })
	s.schedBack.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := gclient.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, "placement: connected", meta.GetActorRuntime().GetPlacement())
	}, time.Second*30, time.Millisecond*10)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*10)
	assert.Greater(t, s.invoked.Load(), invokedBefore)
}
