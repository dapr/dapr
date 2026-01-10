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

package http

import (
	"context"
	"net/http"
	"path"
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
	suite.Register(new(metrics))
}

type metrics struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (m *metrics) Setup(t *testing.T) []framework.Option {
	m.scheduler = scheduler.New(t)

	app := app.New(t,
		app.WithHandlerFunc("/job/", func(w http.ResponseWriter, r *http.Request) {
			switch path.Base(r.URL.Path) {
			case "success":
				w.WriteHeader(http.StatusOK)
			case "nonretriablefailure":
				w.WriteHeader(http.StatusNotFound)
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
		}),
	)

	m.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(m.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("my_app"),
	)

	return []framework.Option{
		framework.WithProcesses(m.scheduler, m.daprd, app),
	}
}

func (m *metrics) Run(t *testing.T, ctx context.Context) {
	m.scheduler.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	t.Run("successful trigger", func(t *testing.T) {
		body := strings.NewReader(`{"dueTime":"0s","data":"test","repeats":3,"schedule":"@every 0s"}`)
		m.daprd.HTTPPost2xx(t, ctx, "/v1.0-alpha1/jobs/success", body)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := m.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 3, int(metrics["dapr_component_job_success_count|app_id:my_app|namespace:|operation:job_trigger_op"]))
			assert.NotNil(c, metrics["dapr_component_job_latencies_sum|app_id:my_app|namespace:|operation:job_trigger_op"])
		}, time.Second*10, time.Millisecond*10)
	})

	t.Run("non-retriable failed trigger", func(t *testing.T) {
		body := strings.NewReader(`{"dueTime":"0s","data":"test","failure_policy":{"constant":{"interval":"1s","max_retries":3}}}`)
		m.daprd.HTTPPost2xx(t, ctx, "/v1.0-alpha1/jobs/nonretriablefailure", body)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := m.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 1, int(metrics["dapr_component_job_failure_count|app_id:my_app|namespace:|operation:job_trigger_op"]))
			assert.NotNil(c, metrics["dapr_component_job_latencies_sum|app_id:my_app|namespace:|operation:job_trigger_op"])
		}, time.Second*10, time.Millisecond*10)
	})

	t.Run("retriable failed trigger", func(t *testing.T) {
		body := strings.NewReader(`{"dueTime":"0s","data":"test","failure_policy":{"constant":{"interval":"1s","max_retries":3}}}`)
		m.daprd.HTTPPost2xx(t, ctx, "/v1.0-alpha1/jobs/retriablefailure", body)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := m.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 4, int(metrics["dapr_component_job_failure_count|app_id:my_app|namespace:|operation:job_trigger_op"]))
			assert.NotNil(c, metrics["dapr_component_job_latencies_sum|app_id:my_app|namespace:|operation:job_trigger_op"])
		}, time.Second*10, time.Millisecond*10)
	})
}
