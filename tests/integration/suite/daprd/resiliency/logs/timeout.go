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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(timeout))
}

// timeout checks that a timeout that fires is logged with the policy name:
// the first one of an operation at warning level, later ones at debug level.
type timeout struct {
	caller  *daprd.Daprd
	target  *daprd.Daprd
	logline *logline.LogLine

	calls atomic.Int32
}

func (o *timeout) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		o.calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second * 5):
		}
	})
	app := prochttp.New(t, prochttp.WithHandler(handler))

	o.target = daprd.New(t, daprd.WithAppPort(app.Port()))

	o.logline = logline.New(t, logline.WithCaptureAll())
	o.caller = daprd.New(t,
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    timeouts:
      downstream: 200ms
    retries:
      threeTimes:
        policy: constant
        duration: 10ms
        maxRetries: 3
  targets:
    apps:
      `+o.target.AppID()+`:
        timeout: downstream
        retry: threeTimes
`),
		daprd.WithExecOptions(
			exec.WithStdout(o.logline.Stdout()),
			exec.WithStderr(o.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(app, o.logline, o.target, o.caller),
	}
}

func (o *timeout) Run(t *testing.T, ctx context.Context) {
	o.caller.WaitUntilRunning(t, ctx)
	o.target.WaitUntilRunning(t, ctx)

	id := o.target.AppID()
	assert.Equal(t, http.StatusInternalServerError, invoke(t, ctx, o.caller, o.target, "slow"))

	timedOut := "Resiliency timeout (downstream): operation endpoint[" + id + ", " + id + ":slow] timed out after 200ms"
	retrying := "Resiliency retry (threeTimes): error processing operation endpoint[" + id + ", " + id + ":slow]. Retrying in 10ms…"
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(4), o.calls.Load())
		assert.Equal(c, 1, countLines(o.logline, "warning", timedOut))
		assert.Equal(c, 3, countLines(o.logline, "debug", timedOut))
		assert.Equal(c, 1, countLines(o.logline, "warning", retrying))
		assert.Equal(c, 2, countLines(o.logline, "debug", retrying))
	}, time.Second*10, time.Millisecond*10)
}
