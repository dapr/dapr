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
	suite.Register(new(reload))
}

type reload struct {
	daprd      *daprd.Daprd
	configFile string
	log        *log.Log
}

func (r *reload) Setup(t *testing.T) []framework.Option {
	r.log = log.New()

	r.configFile = os.WriteFileYaml(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sighup-reload
spec:
  metrics:
    http:
      increasedCardinality: false
`)

	testApp := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	r.daprd = daprd.New(t,
		daprd.WithAppPort(testApp.Port()),
		daprd.WithConfigs(r.configFile),
		daprd.WithExecOptions(exec.WithStdout(r.log), exec.WithStderr(r.log)),
	)

	return []framework.Option{
		framework.WithProcesses(testApp, r.daprd),
	}
}

func (r *reload) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, r.log.Contains(`increasedCardinality\":false`),
			"expected log to show increasedCardinality:false")
	}, 5*time.Second, 10*time.Millisecond)

	os.WriteFileTo(t, r.configFile, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sighup-reload
spec:
  metrics:
    http:
      increasedCardinality: true
`)

	r.log.Reset()
	r.daprd.SignalHUP(t)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, r.log.Contains(`increasedCardinality\":true`),
			"expected log to show increasedCardinality:true after SIGHUP")
	}, 10*time.Second, 10*time.Millisecond)
}
