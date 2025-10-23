/*
Copyright 2025 The Dapr Authors
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
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appapitoken))
}

type appapitoken struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	headerCh  chan nethttp.Header
}

func (a *appapitoken) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)
	a.headerCh = make(chan nethttp.Header, 1)

	app := app.New(t,
		app.WithHandlerFunc("/job/test-job", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			a.headerCh <- r.Header.Clone()
			io.ReadAll(r.Body)
			w.WriteHeader(nethttp.StatusOK)
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppAPIToken(t, "test-job-app-token"),
		daprd.WithSchedulerAddresses(a.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(a.scheduler, app, a.daprd),
	}
}

func (a *appapitoken) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	postURL := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/jobs/test-job", a.daprd.HTTPPort())
	body := `{"schedule": "@every 1s", "data": {"message": "test"}}`
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, postURL, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	io.ReadAll(resp.Body)
	resp.Body.Close()

	select {
	case headers := <-a.headerCh:
		token := headers.Get("dapr-api-token")
		assert.NotEmpty(t, token)
		assert.Equal(t, "test-job-app-token", token)
	case <-time.After(time.Second * 10):
		assert.Fail(t, "Timed out waiting for job event to be delivered to app")
	}
}
