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

package reminders

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(restartdelivery))
}

// restartdelivery asserts that reminder DELIVERY resumes within seconds of a
// scheduler restart under active reminder load. Recovery regressions on
// either side of the stream (server pool routing to dead streams, client
// reconnect stalls) historically showed up as multi-minute delivery outages
// after the scheduler came back healthy, so the resume bound is the
// regression assertion. The restart is graceful: a hard kill leaves the
// previous process's cron leadership lease in the persisted etcd data and
// the restarted engine must wait out the lease TTL, which would swamp the
// bound this test exists to enforce.
type restartdelivery struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler

	called atomic.Int64
}

func (r *restartdelivery) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/tick", func(http.ResponseWriter, *http.Request) {
		r.called.Add(1)
	})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	r.place = placement.New(t)
	r.sched = scheduler.New(t)
	r.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithScheduler(r.sched),
	)

	return []framework.Option{
		framework.WithProcesses(r.place, srv),
	}
}

func (r *restartdelivery) Run(t *testing.T, ctx context.Context) {
	// The scheduler and daprd are run here rather than as framework processes
	// so the test owns the scheduler's lifecycle and daprd starts against a
	// live scheduler.
	r.sched.Run(t, ctx)
	t.Cleanup(func() { r.sched.Kill(t) })
	r.sched.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)

	r.daprd.Run(t, ctx)
	t.Cleanup(func() { r.daprd.Cleanup(t) })
	r.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	daprdURL := "http://localhost:" + strconv.Itoa(r.daprd.HTTPPort()) + "/v1.0/actors/myactortype/myactorid"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL+"/method/foo", nil)
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rErr := httpClient.Do(req)
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, 10*time.Second, 10*time.Millisecond, "actor not ready in time")

	_, err = r.daprd.GRPCClient(t, ctx).RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "tick",
		DueTime:   "0s",
		Period:    "1s",
	})
	require.NoError(t, err)

	// Steady state: the reminder is firing every second.
	require.Eventually(t, func() bool {
		return r.called.Load() >= 3
	}, 15*time.Second, 10*time.Millisecond, "reminder never reached steady delivery")

	before := r.called.Load()
	r.sched.RestartGraceful(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)

	// The regression bound: delivery must resume within seconds of the
	// scheduler being healthy again, not minutes.
	require.Eventually(t, func() bool {
		return r.called.Load() > before
	}, 15*time.Second, 10*time.Millisecond,
		"reminder delivery did not resume within 15s of the scheduler restart")
}
