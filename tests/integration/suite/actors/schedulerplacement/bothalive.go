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
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bothalive))
}

// bothalive runs the placement service and a scheduler serving placement at
// once: the sidecar follows the scheduler's advertisement and the placement
// service never sees a host.
type bothalive struct {
	sched *scheduler.Scheduler
	place *placement.Placement
	daprd *daprd.Daprd

	invoked atomic.Int64
}

func (b *bothalive) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
		b.invoked.Add(1)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	b.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	b.place = placement.New(t)
	b.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(b.sched),
		daprd.WithPlacementAddresses(b.place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(b.sched, b.place, srv, b.daprd),
	}
}

func (b *bothalive) Run(t *testing.T, ctx context.Context) {
	b.sched.WaitUntilRunning(t, ctx)
	b.place.WaitUntilRunning(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "foo",
		})
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*10)
	assert.Positive(t, b.invoked.Load())

	// The scheduler holds the placement stream and the placement service
	// holds none.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var streams float64
		for k, v := range b.sched.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(c, streams, float64(1))
	}, time.Second*10, time.Millisecond*50)

	var runtimes float64
	for k, v := range b.place.Metrics(t, ctx).All() {
		if strings.HasPrefix(k, "dapr_placement_runtimes_total") {
			runtimes += v
		}
	}
	assert.Zero(t, runtimes, "the placement service must never see a host while the scheduler serves placement")
}
