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
	"strconv"
	"strings"
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
	triggered atomic.Int64
}

func (r *recovery) Setup(t *testing.T) []framework.Option {
	r.scheduler = scheduler.New(t)

	app := prochttp.New(t, prochttp.WithHandlerFunc("/job/", func(w http.ResponseWriter, _ *http.Request) {
		r.triggered.Add(1)
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
		assert.Equal(c, int64(1), r.triggered.Load())
	}, time.Second*10, time.Millisecond*10)

	r.scheduler.Restart(t, ctx)
	r.scheduler.WaitUntilRunning(t, ctx)

	const stableObservations = 20
	var consecutive int
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		got := int(r.scheduler.Metrics(c, ctx).All()["dapr_scheduler_sidecars_connected"])
		if got != 1 {
			consecutive = 0
			assert.Equal(c, 1, got)
			return
		}
		consecutive++
		assert.GreaterOrEqual(c, consecutive, stableObservations)
	}, time.Second*60, time.Millisecond*50)

	attempt := 0
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		attempt++
		r.daprd.HTTPPost2xx(c, ctx, "/v1.0/jobs/post-"+strconv.Itoa(attempt), strings.NewReader(`{"dueTime":"0s"}`))
	}, time.Second*60, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), r.triggered.Load())
	}, time.Second*10, time.Millisecond*10)
	time.Sleep(2 * time.Second)
	assert.Equal(t, int64(2), r.triggered.Load())
}
