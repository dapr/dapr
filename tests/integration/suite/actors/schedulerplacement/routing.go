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
	"strconv"
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
	suite.Register(new(routing))
}

// routing tests that reminder triggers are routed directly to the placement
// owner host: with two hosts of the same actor type, every reminder fires
// exactly once and neither daprd forwards reminders to the other over the
// internal CallActorReminder RPC.
type routing struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	sched  *scheduler.Scheduler

	called1 atomic.Int64
	called2 atomic.Int64
}

func (r *routing) Setup(t *testing.T) []framework.Option {
	newApp := func(counter *atomic.Int64) *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/myactortype/", func(_ http.ResponseWriter, req *http.Request) {
			if strings.Contains(req.URL.Path, "/method/remind/") {
				counter.Add(1)
			}
		})
		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	srv1 := newApp(&r.called1)
	srv2 := newApp(&r.called2)

	r.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	r.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv1.Port()),
		daprd.WithScheduler(r.sched),
	)
	r.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppPort(srv2.Port()),
		daprd.WithScheduler(r.sched),
	)

	return []framework.Option{
		framework.WithProcesses(r.sched, srv1, srv2, r.daprd1, r.daprd2),
	}
}

func (r *routing) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)
	r.daprd1.WaitUntilRunning(t, ctx)
	r.daprd2.WaitUntilRunning(t, ctx)

	// Both daprds' WatchJobs streams must be registered before a 0s reminder
	// can fire: a trigger during the startup window may land on the
	// non-owner and be forwarded, and zero forwarding is a steady state
	// claim.
	r.sched.WaitUntilSidecarsConnected(t, ctx, 6)

	gclient := r.daprd1.GRPCClient(t, ctx)

	const numReminders = 50
	for i := range numReminders {
		_, err := gclient.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "myactortype",
			ActorId:   "actor-" + strconv.Itoa(i),
			Name:      "myreminder",
			DueTime:   "0s",
		})
		require.NoError(t, err)
	}

	assert.Eventually(t, func() bool {
		return r.called1.Load()+r.called2.Load() >= numReminders
	}, time.Second*20, time.Millisecond*10)

	// Rendezvous hashing splits 50 actor IDs over 2 hosts; both must have
	// fired reminders.
	assert.Positive(t, r.called1.Load())
	assert.Positive(t, r.called2.Load())

	// No reminder was forwarded between daprds over the internal
	// CallActorReminder RPC: the scheduler delivered each trigger to the
	// owner host directly.
	for _, d := range []*daprd.Daprd{r.daprd1, r.daprd2} {
		for k, v := range d.Metrics(t, ctx).All() {
			if strings.Contains(k, "CallActorReminder") {
				assert.Zerof(t, v, "unexpected reminder forwarding metric %q", k)
			}
		}
	}
}
