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

package metrics

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(retriablefailure))
}

type retriablefailure struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (r *retriablefailure) Setup(t *testing.T) []framework.Option {
	r.scheduler = scheduler.New(t)

	app := app.New(t,
		app.WithHandlerFunc("/job/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	r.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(r.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("my_app"),
	)

	return []framework.Option{
		framework.WithProcesses(r.scheduler, r.daprd, app),
	}
}

func (r *retriablefailure) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	t.Run("retriable failed job trigger", func(t *testing.T) {
		body := strings.NewReader(`{"dueTime":"0s","data":"test","failure_policy":{"constant":{"interval":"0s","max_retries":3}}}`)
		r.daprd.HTTPPost2xx(t, ctx, "/v1.0-alpha1/jobs/retriablefailure", body)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := r.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 4, int(metrics["dapr_component_job_failure_count|app_id:my_app|namespace:|operation:job_trigger_op"]))
			assert.NotNil(c, metrics["dapr_component_job_latencies_sum|app_id:my_app|namespace:|operation:job_trigger_op"])
		}, time.Second*10, time.Millisecond*10)
	})
}
