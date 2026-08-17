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

package hostsreload

import (
	"context"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(recovery))
}

type recovery struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	triggers  sync.Map // job name -> *atomic.Int64
}

func (r *recovery) count(name string) int64 {
	v, ok := r.triggers.Load(name)
	if !ok {
		return 0
	}
	return v.(*atomic.Int64).Load()
}

func (r *recovery) Setup(t *testing.T) []framework.Option {
	r.scheduler = scheduler.New(t)

	app := prochttp.New(t, prochttp.WithHandlerFunc("/job/", func(w http.ResponseWriter, req *http.Request) {
		v, _ := r.triggers.LoadOrStore(path.Base(req.URL.Path), new(atomic.Int64))
		v.(*atomic.Int64).Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	r.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
	)

	return []framework.Option{
		framework.WithProcesses(r.scheduler, app, r.daprd),
	}
}

func (r *recovery) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	r.daprd.HTTPPost2xx(t, ctx, "/v1.0/jobs/pre", strings.NewReader(`{"dueTime":"0s"}`))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), r.count("pre"))
	}, time.Second*10, time.Millisecond*10)

	r.scheduler.Restart(t, ctx)
	r.scheduler.WaitUntilRunning(t, ctx)

	// The scheduling retry loop below is itself the settling probe: attempts
	// rejected while daprd's scheduler clients are still reconnecting
	// register nothing (unique name per attempt, so exactly one job is ever
	// accepted). The window is sized for daprd's WatchHosts reconvergence
	// after a scheduler restart, which takes roughly 20s today (pre-existing,
	// a follow-up candidate). Deliberately NOT gated on scheduler metrics or
	// etcd leadership: the framework's per-process HTTP client can serve
	// stale keep-alive connections across a restart and both helpers flake
	// on slow runners.
	attempt := 0
	accepted := ""
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		attempt++
		name := "post-" + strconv.Itoa(attempt)
		r.daprd.HTTPPost2xx(c, ctx, "/v1.0/jobs/"+name, strings.NewReader(`{"dueTime":"0s"}`))
		accepted = name
	}, time.Second*60, time.Millisecond*10)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, r.count(accepted), int64(1))
	}, time.Second*10, time.Millisecond*10)

	// Exactly-once is asserted on a FINAL job scheduled once the pipeline is
	// proven healthy (the post-N acceptance above). The recovery probes
	// themselves get at-least-once tolerance: a scheduling attempt that
	// times out client-side can still have registered server-side, so a
	// retried attempt may legitimately create a second job.
	r.daprd.HTTPPost2xx(t, ctx, "/v1.0/jobs/settled", strings.NewReader(`{"dueTime":"0s"}`))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), r.count("settled"))
	}, time.Second*10, time.Millisecond*10)
	time.Sleep(2 * time.Second)
	assert.Equal(t, int64(1), r.count("settled"),
		"a job on the settled pipeline must trigger exactly once: a spurious hosts rebuild would tear down fresh streams and redeliver")
}
