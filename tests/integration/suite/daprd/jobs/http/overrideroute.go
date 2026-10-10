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
	"encoding/json"
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
	suite.Register(new(overrideroute))
}

// overrideroute asserts that a job scheduled over the HTTP API with an
// `overrideRoutePath` is delivered to the HTTP app on
// `/job/{overrideRoutePath}` rather than `/job/{name}`.
type overrideroute struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	pathCh    chan string
}

func (o *overrideroute) Setup(t *testing.T) []framework.Option {
	o.scheduler = scheduler.New(t)

	o.pathCh = make(chan string, 1)
	app := app.New(t,
		app.WithHandlerFunc("/job/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			_, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			o.pathCh <- r.URL.Path
		}),
	)

	o.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(o.scheduler.Address()),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
	)

	return []framework.Option{
		framework.WithProcesses(o.scheduler, app, o.daprd),
	}
}

func (o *overrideroute) Run(t *testing.T, ctx context.Context) {
	o.scheduler.WaitUntilRunning(t, ctx)
	o.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	t.Run("job is delivered on the override route path", func(t *testing.T) {
		o.daprd.HTTPPost2xx(t, ctx, "/v1.0/jobs/sync-video-state-123",
			strings.NewReader(`{"dueTime":"0s","overrideRoutePath":"sync-video-state/123"}`),
		)

		select {
		case path := <-o.pathCh:
			assert.Equal(t, "/job/sync-video-state/123", path)
		case <-time.After(time.Second * 10):
			require.Fail(t, "timed out waiting for triggered job")
		}
	})

	t.Run("job without override route path is delivered on its name", func(t *testing.T) {
		o.daprd.HTTPPost2xx(t, ctx, "/v1.0/jobs/no-override",
			strings.NewReader(`{"dueTime":"0s"}`),
		)

		select {
		case path := <-o.pathCh:
			assert.Equal(t, "/job/no-override", path)
		case <-time.After(time.Second * 10):
			require.Fail(t, "timed out waiting for triggered job")
		}
	})

	t.Run("override route path is returned by get", func(t *testing.T) {
		o.daprd.HTTPPost2xx(t, ctx, "/v1.0/jobs/with-override",
			strings.NewReader(`{"schedule":"@daily","overrideRoutePath":"my/route"}`),
		)

		getURL := fmt.Sprintf("http://%s/v1.0/jobs/with-override", o.daprd.HTTPAddress())
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, getURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, nethttp.StatusOK, resp.StatusCode, string(body))

		var job map[string]any
		require.NoError(t, json.Unmarshal(body, &job))
		assert.Equal(t, "my/route", job["overrideRoutePath"])
	})

	t.Run("invalid override route path is rejected", func(t *testing.T) {
		postURL := fmt.Sprintf("http://%s/v1.0/jobs/invalid", o.daprd.HTTPAddress())
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, postURL,
			strings.NewReader(`{"schedule":"@daily","overrideRoutePath":"../escape"}`),
		)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, nethttp.StatusBadRequest, resp.StatusCode, string(body))

		var data map[string]any
		require.NoError(t, json.Unmarshal(body, &data))
		assert.Equal(t, "DAPR_SCHEDULER_JOB_OVERRIDE_ROUTE_PATH", data["errorCode"])
		assert.Equal(t, `invalid job override route path: "../escape" must not contain '.' or '..' path segments`, data["message"])
	})
}
