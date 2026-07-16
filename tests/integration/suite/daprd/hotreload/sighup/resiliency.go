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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(resiliencysighup))
}

// resiliencysighup tests that resiliency resources are reloaded when daprd
// receives a SIGHUP signal in selfhosted mode.
type resiliencysighup struct {
	daprd         *daprd.Daprd
	logOut        *log.Log
	resiliencyDir string
}

func (r *resiliencysighup) Setup(t *testing.T) []framework.Option {
	r.logOut = log.New()
	r.resiliencyDir = t.TempDir()

	configFile := os.WriteFileYaml(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sighup-resiliency
spec: {}
`)

	r.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(r.resiliencyDir),
		daprd.WithExecOptions(exec.WithStdout(r.logOut), exec.WithStderr(r.logOut)),
	)

	return []framework.Option{
		framework.WithProcesses(r.daprd),
	}
}

func (r *resiliencysighup) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	t.Run("resiliency loaded after SIGHUP", func(t *testing.T) {
		os.WriteFileTo(t, r.resiliencyDir+"/resiliency.yaml", `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    timeouts:
      general: 5s
  targets: {}
`)

		r.logOut.Reset()
		r.daprd.SignalHUP(t)
		r.daprd.WaitUntilRunning(t, ctx)

		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			assert.True(ct, r.logOut.Contains("myresiliency"),
				"expected log to contain resiliency name after SIGHUP")
		}, 20*time.Second, 10*time.Millisecond)
	})
}
