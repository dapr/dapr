/*
Copyright 2024 The Dapr Authors
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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(daprconnections))
}

type daprconnections struct {
	scheduler *scheduler.Scheduler
	daprd     *daprd.Daprd

	connectionlogline *logline.LogLine
}

func (c *daprconnections) Setup(t *testing.T) []framework.Option {
	c.connectionlogline = logline.New(t,
		logline.WithStdoutLineContains(
			`Adding a Sidecar connection to Scheduler for appID: default/C`,
		),
	)
	c.scheduler = scheduler.New(t,
		scheduler.WithLogLevel("debug"),
		scheduler.WithExecOptions(
			exec.WithStdout(c.connectionlogline.Stdout()),
		),
	)
	srv := app.New(t)
	c.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(c.scheduler.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppID("C"),
	)

	return []framework.Option{
		framework.WithProcesses(c.connectionlogline, srv, c.scheduler, c.daprd),
	}
}

func (c *daprconnections) Run(t *testing.T, ctx context.Context) {
	c.scheduler.WaitUntilRunning(t, ctx)
	c.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)

	t.Run("ensure dapr connection with scheduler metric", func(t *testing.T) {
		c.assertMetricExists(t, ctx, client, "dapr_scheduler_sidecars_connected_total", 1)
	})

	c.connectionlogline.EventuallyFoundAll(t)
}

// assert the metric exists and the count is correct
func (c *daprconnections) assertMetricExists(t *testing.T, ctx context.Context, client *http.Client, expectedMetric string, expectedCount int) {
	t.Helper()

	metricReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", c.scheduler.MetricsPort()), nil)
	require.NoError(t, err)

	resp, err := client.Do(metricReq)
	require.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	foundMetric := false

	for _, line := range bytes.Split(respBody, []byte("\n")) {
		if len(line) == 0 || line[0] == '#' {
			continue
		}

		split := bytes.Split(line, []byte(" "))
		if len(split) != 2 { // nolint:mnd
			continue
		}

		// dapr_scheduler_sidecars_connected_total{app_id="appid"}
		metricName := string(split[0])
		metricVal := string(split[1])
		if !strings.Contains(metricName, expectedMetric) {
			continue
		}
		if strings.Contains(metricName, expectedMetric) {
			metricCount, err := strconv.Atoi(metricVal)
			require.NoError(t, err)
			assert.Equal(t, expectedCount, metricCount)
			foundMetric = true
			break
		}
	}
	assert.True(t, foundMetric, "Expected metric %s not found", expectedMetric)
}
