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

package scheduler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unreachable))
}

// unreachable starts daprd with a Scheduler address that refuses every
// connection. daprd stays up and not ready, logs why with the fix, and
// reports the Scheduler address, without the error text, in the healthz
// response body.
type unreachable struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
	address string
}

func (u *unreachable) Setup(t *testing.T) []framework.Option {
	// The ports process frees the port before daprd starts, so nothing
	// listens on the Scheduler address.
	fp := ports.Reserve(t, 1)
	u.address = "127.0.0.1:" + strconv.Itoa(fp.Port(t))

	u.logline = logline.New(t, logline.WithStdoutLineContains(
		"Cannot connect to the Scheduler at "+u.address+" for ",
		"daprd is not ready until it connects",
		"Start the Scheduler, or set --scheduler-host-address (on Kubernetes, the dapr.io/scheduler-host-address annotation) to the address of a running Scheduler",
		"https://docs.dapr.io/concepts/dapr-services/scheduler/",
	))

	u.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(u.address),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_SCHEDULER_TEST_UNREACHABLE_REPORT_DELAY", "100ms"),
			exec.WithStdout(u.logline.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(fp, u.logline, u.daprd),
	}
}

func (u *unreachable) Run(t *testing.T, ctx context.Context) {
	u.daprd.WaitUntilTCPReady(t, ctx)

	httpClient := client.HTTP(t)
	url := fmt.Sprintf("http://%s/v1.0/healthz", u.daprd.HTTPAddress())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(c, err)
		resp, err := httpClient.Do(req)
		require.NoError(c, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(c, err)
		assert.Equal(c, http.StatusInternalServerError, resp.StatusCode)
		assert.Contains(c, string(body), "ERR_HEALTH_NOT_READY")
		assert.Contains(c, string(body), "scheduler-watch-hosts: cannot connect to the Scheduler at "+u.address+", see the daprd log")
		// The healthz endpoint needs no API token, so it must not show the
		// error text.
		assert.NotContains(c, string(body), "dial tcp")
	}, 5*time.Second, 10*time.Millisecond)

	u.logline.EventuallyFoundAll(t)
}
