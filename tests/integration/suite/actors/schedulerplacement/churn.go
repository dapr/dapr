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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(churn))
}

// churn joins and removes sidecars under scheduler placement while two
// survivors keep hosting: actor invocations succeed through every
// membership change, with no sidecar restart.
type churn struct {
	sched     *scheduler.Scheduler
	survivors [2]*daprd.Daprd
	cyclers   [2]*daprd.Daprd
}

func (h *churn) Setup(t *testing.T) []framework.Option {
	newApp := func() *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		handler.HandleFunc("/actors/myactortype/", func(w http.ResponseWriter, r *http.Request) {})
		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	h.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))

	processes := make([]process.Interface, 0, 1+len(h.survivors)*2+len(h.cyclers))
	processes = append(processes, h.sched)
	for i := range h.survivors {
		srv := newApp()
		h.survivors[i] = daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(srv.Port()),
			daprd.WithScheduler(h.sched),
		)
		processes = append(processes, srv, h.survivors[i])
	}
	for i := range h.cyclers {
		srv := newApp()
		h.cyclers[i] = daprd.New(t,
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithAppPort(srv.Port()),
			daprd.WithScheduler(h.sched),
		)
		processes = append(processes, srv)
	}

	return []framework.Option{
		framework.WithProcesses(processes...),
	}
}

func (h *churn) Run(t *testing.T, ctx context.Context) {
	h.sched.WaitUntilRunning(t, ctx)
	for _, d := range h.survivors {
		d.WaitUntilRunning(t, ctx)
	}

	gclient := h.survivors[0].GRPCClient(t, ctx)
	invokeAll := func() {
		for i := range 8 {
			id := "actor-" + strconv.Itoa(i)
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
					ActorType: "myactortype",
					ActorId:   id,
					Method:    "foo",
				})
				assert.NoError(c, err)
			}, time.Second*20, time.Millisecond*10)
		}
	}

	invokeAll()

	// Two sidecars join: invocations keep succeeding through the rebalance.
	for _, d := range h.cyclers {
		d.Run(t, ctx)
		t.Cleanup(func() { d.Cleanup(t) })
	}
	for _, d := range h.cyclers {
		d.WaitUntilRunning(t, ctx)
	}
	invokeAll()

	// The joined sidecars leave: every invocation still succeeds.
	for _, d := range h.cyclers {
		d.Cleanup(t)
	}
	invokeAll()
}
