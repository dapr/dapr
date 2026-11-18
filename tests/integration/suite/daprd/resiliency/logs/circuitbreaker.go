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

package logs

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(circuitbreaker))
}

// circuitbreaker checks that circuit breaker state changes are logged with
// the policy name, with the breaker opening at warning level.
type circuitbreaker struct {
	caller  *daprd.Daprd
	target  *daprd.Daprd
	logline *logline.LogLine

	fail atomic.Bool
}

func (c *circuitbreaker) Setup(t *testing.T) []framework.Option {
	c.fail.Store(true)
	handler := http.NewServeMux()
	handler.HandleFunc("/cb", func(w http.ResponseWriter, _ *http.Request) {
		if c.fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	app := prochttp.New(t, prochttp.WithHandler(handler))

	c.target = daprd.New(t, daprd.WithAppPort(app.Port()))

	c.logline = logline.New(t, logline.WithCaptureAll())
	c.caller = daprd.New(t,
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    circuitBreakers:
      myCB:
        maxRequests: 1
        timeout: 1s
        trip: consecutiveFailures >= 2
  targets:
    apps:
      `+c.target.AppID()+`:
        circuitBreaker: myCB
`),
		daprd.WithExecOptions(
			exec.WithStdout(c.logline.Stdout()),
			exec.WithStderr(c.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(app, c.logline, c.target, c.caller),
	}
}

func (c *circuitbreaker) Run(t *testing.T, ctx context.Context) {
	c.caller.WaitUntilRunning(t, ctx)
	c.target.WaitUntilRunning(t, ctx)

	// The breaker's name is the endpoint, which the log line quotes.
	stateChange := func(from, to string) []string {
		return []string{
			"Resiliency circuit breaker (myCB): ",
			c.target.AppID() + `:cb\" changed state from ` + from + " to " + to + `"`,
		}
	}

	assert.Equal(t, http.StatusInternalServerError, invoke(t, ctx, c.caller, c.target, "cb"))
	assert.Equal(t, http.StatusInternalServerError, invoke(t, ctx, c.caller, c.target, "cb"))
	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.Equal(ct, 1, countLines(c.logline, "warning", stateChange("closed", "open")...))
	}, time.Second*10, time.Millisecond*10)

	// After the breaker's timeout, the next successful call goes through the
	// half-open state and closes the breaker.
	c.fail.Store(false)
	require.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.Equal(ct, http.StatusOK, invoke(t, ctx, c.caller, c.target, "cb"))
	}, time.Second*10, time.Millisecond*100)

	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		assert.Equal(ct, 1, countLines(c.logline, "info", stateChange("open", "half-open")...))
		assert.Equal(ct, 1, countLines(c.logline, "info", stateChange("half-open", "closed")...))
	}, time.Second*10, time.Millisecond*10)
}
