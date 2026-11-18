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
	suite.Register(new(retry))
}

// retry checks that the first retry of an operation is logged at warning
// level with the policy name, later retries at debug level, and the recovery
// at info level.
type retry struct {
	caller  *daprd.Daprd
	target  *daprd.Daprd
	logline *logline.LogLine

	recoverCalls atomic.Int32
	failCalls    atomic.Int32
}

func (r *retry) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/recover", func(w http.ResponseWriter, _ *http.Request) {
		if r.recoverCalls.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	handler.HandleFunc("/fail", func(w http.ResponseWriter, _ *http.Request) {
		r.failCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	app := prochttp.New(t, prochttp.WithHandler(handler))

	r.target = daprd.New(t, daprd.WithAppPort(app.Port()))

	r.logline = logline.New(t, logline.WithCaptureAll())
	r.caller = daprd.New(t,
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    retries:
      threeTimes:
        policy: constant
        duration: 10ms
        maxRetries: 3
  targets:
    apps:
      `+r.target.AppID()+`:
        retry: threeTimes
`),
		daprd.WithExecOptions(
			exec.WithStdout(r.logline.Stdout()),
			exec.WithStderr(r.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(app, r.logline, r.target, r.caller),
	}
}

func (r *retry) Run(t *testing.T, ctx context.Context) {
	r.caller.WaitUntilRunning(t, ctx)
	r.target.WaitUntilRunning(t, ctx)

	id := r.target.AppID()

	t.Run("recovers after two retries", func(t *testing.T) {
		require.Equal(t, http.StatusOK, invoke(t, ctx, r.caller, r.target, "recover"))
		assert.Equal(t, int32(3), r.recoverCalls.Load())

		retrying := "Resiliency retry (threeTimes): error processing operation endpoint[" + id + ", " + id + ":recover]. Retrying in 10ms…"
		recovered := "Resiliency retry (threeTimes): recovered processing operation endpoint[" + id + ", " + id + ":recover] after 3 attempts"
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, 1, countLines(r.logline, "warning", retrying))
			assert.Equal(c, 1, countLines(r.logline, "debug", retrying))
			assert.Equal(c, 1, countLines(r.logline, "info", recovered))
		}, time.Second*10, time.Millisecond*10)
	})

	t.Run("one warning when retries are exhausted", func(t *testing.T) {
		require.Equal(t, http.StatusInternalServerError, invoke(t, ctx, r.caller, r.target, "fail"))
		assert.Equal(t, int32(4), r.failCalls.Load())

		retrying := "Resiliency retry (threeTimes): error processing operation endpoint[" + id + ", " + id + ":fail]. Retrying in 10ms…"
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, 1, countLines(r.logline, "warning", retrying))
			assert.Equal(c, 2, countLines(r.logline, "debug", retrying))
		}, time.Second*10, time.Millisecond*10)
		assert.Zero(t, countLines(r.logline, "info", "recovered processing operation endpoint["+id+", "+id+":fail]"))
	})
}
