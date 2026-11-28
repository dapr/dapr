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
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/slow", o.caller.HTTPPort(), id), nil)
	require.NoError(t, err)
	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	timedOut := `level=%s msg="Resiliency timeout (downstream): operation endpoint[` + id + ", " + id + `:slow] timed out after 200ms (attempt %d)"`
	retrying := `level=%s msg="Resiliency retry (threeTimes): error processing operation endpoint[` + id + ", " + id + `:slow] (attempt %d). Retrying in 10ms…"`
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(4), o.calls.Load())
		assert.Equal(c, 1, o.logline.Count(fmt.Sprintf(timedOut, "warning", 1)))
		assert.Equal(c, 1, o.logline.Count(fmt.Sprintf(retrying, "warning", 1)))
		for attempt := 2; attempt <= 4; attempt++ {
			assert.Equal(c, 1, o.logline.Count(fmt.Sprintf(timedOut, "debug", attempt)))
		}
		for attempt := 2; attempt <= 3; attempt++ {
			assert.Equal(c, 1, o.logline.Count(fmt.Sprintf(retrying, "debug", attempt)))
		}
	}, time.Second*10, time.Millisecond*10)
}
