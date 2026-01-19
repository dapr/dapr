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
	"sync/atomic"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(disabledconfigendpoint))
}

// disabledconfigendpoint tests instantiating daprd with multiple configuration files and no init config.
type disabledconfigendpoint struct {
	daprd             *daprd.Daprd
	logLineAppWaiting *logline.LogLine
	configCalled      atomic.Bool
}

func (d *disabledconfigendpoint) Setup(t *testing.T) []framework.Option {
	d.logLineAppWaiting = logline.New(t, logline.WithStdoutLineContains(
		"Skipping programmatic dapr configuration loading",
	))

	srv := prochttp.New(t,
		prochttp.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			d.configCalled.Store(true)
			w.Write([]byte(`{}`))
		}),
	)

	d.daprd = daprd.New(t,
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithDisableInitEndpoints("config"),
		daprd.WithExecOptions(exec.WithStdout(d.logLineAppWaiting.Stdout())))

	return []framework.Option{
		framework.WithProcesses(srv, d.daprd, d.logLineAppWaiting),
	}
}

func (d *disabledconfigendpoint) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)
	d.logLineAppWaiting.EventuallyFoundAll(t)
	require.False(t, d.configCalled.Load(), "/dapr/config endpoint should not have been called")
}
