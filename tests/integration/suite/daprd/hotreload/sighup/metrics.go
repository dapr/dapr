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

package sighup

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(metrics))
}

// metrics ensures that the runtime restarted by SIGHUP does not leave the
// metrics of the previous runtime being exported, which makes every scrape
// report duplicate samples and serve stale values.
type metrics struct {
	daprd *daprd.Daprd
	log   *log.Log
}

func (m *metrics) Setup(t *testing.T) []framework.Option {
	m.log = log.New()

	testApp := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	m.daprd = daprd.New(t,
		daprd.WithAppPort(testApp.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("testapp"),
		daprd.WithExecOptions(exec.WithStdout(m.log), exec.WithStderr(m.log)),
	)

	return []framework.Option{
		framework.WithProcesses(testApp, m.daprd),
	}
}

func (m *metrics) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	const key = "dapr_http_server_request_count|app_id:testapp|method:GET|path:/v1.0/invoke/testapp/method/hi|status:200"

	m.daprd.HTTPGet2xx(t, ctx, "/v1.0/invoke/testapp/method/hi")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, int(m.daprd.Metrics(c, ctx).All()[key]))
	}, 10*time.Second, 10*time.Millisecond)

	m.daprd.SignalHUP(t)
	m.daprd.WaitUntilRunning(t, ctx)

	m.daprd.HTTPGet2xx(t, ctx, "/v1.0/invoke/testapp/method/hi")
	m.daprd.HTTPGet2xx(t, ctx, "/v1.0/invoke/testapp/method/hi")

	// The restarted runtime records on its own meter, so its count is the only
	// one exported. A meter left over from the previous runtime would keep
	// serving its own stale count instead.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 2, int(m.daprd.Metrics(c, ctx).All()[key]))
	}, 10*time.Second, 10*time.Millisecond)

	for range 3 {
		m.daprd.Metrics(t, ctx)
	}
	assert.False(t, m.log.Contains("was collected before"),
		"expected no duplicate metric samples to be collected after SIGHUP")
}
