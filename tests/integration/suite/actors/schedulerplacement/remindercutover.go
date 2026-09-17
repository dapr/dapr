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
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(remindercutover))
}

// remindercutover keeps a periodic reminder firing across the cutover: fires
// are due inside the window where the actor rehomes from the placement table
// to the scheduler table, and they must keep landing after it.
type remindercutover struct {
	sched  *scheduler.Scheduler
	place  *placement.Placement
	daprds [3]*daprd.Daprd

	fired atomic.Int64
}

func (r *remindercutover) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	r.place = placement.New(t)

	procs := make([]process.Interface, 0, 2+2*len(r.daprds))
	procs = append(procs, r.sched, r.place)
	for i := range r.daprds {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "/remind/") {
				r.fired.Add(1)
			}
		})
		srv := prochttp.New(t, prochttp.WithHandler(handler))
		r.daprds[i] = daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(srv.Port()),
			daprd.WithScheduler(r.sched),
			daprd.WithPlacementAddresses(r.place.Address()),
		)
		procs = append(procs, srv, r.daprds[i])
	}

	return []framework.Option{framework.WithProcesses(procs...)}
}

func (r *remindercutover) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	for _, d := range r.daprds {
		d.WaitUntilRunning(t, ctx)
	}

	client := r.daprds[0].GRPCClient(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "reminder-actor",
			Name:      "remindme",
			DueTime:   "1s",
			Period:    "1s",
		})
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*10)

	// The reminder fires through the placement service first.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, r.fired.Load())
	}, time.Second*20, time.Millisecond*10)

	r.place.Cleanup(t)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var streams float64
		for k, v := range r.sched.Metrics(c, ctx).All() {
			if strings.HasPrefix(k, "dapr_scheduler_placement_streams_connected") {
				streams += v
			}
		}
		assert.GreaterOrEqual(c, streams, float64(3))
	}, time.Second*15, time.Millisecond*10)

	// Fires keep landing once the scheduler table owns the actor.
	afterCutover := r.fired.Load()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, r.fired.Load(), afterCutover+2)
	}, time.Second*15, time.Millisecond*10)
}
