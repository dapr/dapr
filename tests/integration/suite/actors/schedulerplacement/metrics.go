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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(placementmetrics))
}

// placementmetrics asserts the scheduler exposes placement observability:
// leadership, connected placement streams, dissemination rounds and per
// actor type table updates, on the scheduler's metrics endpoint.
type placementmetrics struct {
	daprd *daprd.Daprd
	sched *scheduler.Scheduler

	invoked atomic.Int64
}

func (m *placementmetrics) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {
		m.invoked.Add(1)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	m.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	m.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(m.sched),
	)

	return []framework.Option{
		framework.WithProcesses(m.sched, srv, m.daprd),
	}
}

func (m *placementmetrics) Run(t *testing.T, ctx context.Context) {
	m.sched.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	gclient := m.daprd.GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype", ActorId: "a1", Method: "foo",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)

	// metric sums values across label sets for the given metric name. The
	// framework keys labelled series as "name|label:value|...".
	metric := func(all map[string]float64, name string) float64 {
		var total float64
		var found bool
		for k, v := range all {
			if k == name || strings.HasPrefix(k, name+"|") {
				total += v
				found = true
			}
		}
		if !found {
			return -1
		}
		return total
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		all := m.sched.Metrics(c, ctx).All()

		// This scheduler is the placement leader.
		assert.Equal(c, 1, int(all["dapr_scheduler_placement_leader"]))

		// The daprd's placement stream is connected in its namespace.
		assert.GreaterOrEqual(c, metric(all, "dapr_scheduler_placement_streams_connected"), float64(1))

		// The daprd joining disseminated at least one round covering its
		// actor type's table update.
		assert.GreaterOrEqual(c, metric(all, "dapr_scheduler_placement_disseminations_total"), float64(1))
		assert.GreaterOrEqual(c, metric(all, "dapr_scheduler_placement_table_updates_total"), float64(1))
	}, time.Second*20, time.Millisecond*100)
}
