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
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(tracing))
}

type tracing struct {
	daprd      *daprd.Daprd
	configFile string
	log        *log.Log
}

func (tr *tracing) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	tr.log = log.New()

	config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sighup-tracing
spec:
  tracing:
    samplingRate: "0"
`
	tr.configFile = os.WriteFileYaml(t, config)

	testApp := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	tr.daprd = daprd.New(t,
		daprd.WithAppPort(testApp.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("testapp"),
		daprd.WithConfigs(tr.configFile),
		daprd.WithExecOptions(exec.WithStdout(tr.log)),
	)

	return []framework.Option{
		framework.WithProcesses(testApp, tr.daprd),
	}
}

func (tr *tracing) Run(t *testing.T, ctx context.Context) {
	tr.daprd.WaitUntilRunning(t, ctx)

	t.Run("initial configuration has sampling rate 0", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, tr.log.Contains("TraceIDRatioBased{0}"),
				"expected log to show TraceIDRatioBased{0} for sampling rate 0")
		}, 5*time.Second, 100*time.Millisecond)
	})

	t.Run("configuration reloaded with sampling rate 1 after SIGHUP", func(t *testing.T) {
		config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sighup-tracing
spec:
  tracing:
    samplingRate: "1"
`
		os.WriteFileTo(t, tr.configFile, config)

		tr.log.Reset()
		tr.daprd.SignalHUP(t)
		tr.daprd.WaitUntilRunning(t, ctx)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, tr.log.Contains("AlwaysOnSampler"),
				"expected log to show AlwaysOnSampler for sampling rate 1 after SIGHUP")
		}, 10*time.Second, 100*time.Millisecond)
	})
}
