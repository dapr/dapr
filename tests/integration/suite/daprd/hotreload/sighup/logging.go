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
	suite.Register(new(logging))
}

type logging struct {
	daprd      *daprd.Daprd
	configFile string
	log        *log.Log
}

func (l *logging) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	l.log = log.New()

	config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sighup-logging
spec:
  logging:
    apiLogging:
      enabled: true
      obfuscateURLs: false
`
	l.configFile = os.WriteFileYaml(t, config)

	testApp := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	l.daprd = daprd.New(t,
		daprd.WithAppPort(testApp.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("testapp"),
		daprd.WithConfigs(l.configFile),
		daprd.WithExecOptions(exec.WithStdout(l.log)),
	)

	return []framework.Option{
		framework.WithProcesses(testApp, l.daprd),
	}
}

func (l *logging) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)

	t.Run("API logs are not obfuscated before SIGHUP", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			l.daprd.HTTPGet2xx(c, ctx, "/v1.0/invoke/testapp/method/hi")
			assert.True(c, l.log.Contains("/v1.0/invoke/testapp/method/hi"),
				"expected logs to contain the full path when obfuscateURLs is false")
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("API logs are obfuscated after SIGHUP", func(t *testing.T) {
		config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sighup-logging
spec:
  logging:
    apiLogging:
      enabled: true
      obfuscateURLs: true
`
		os.WriteFileTo(t, l.configFile, config)

		l.log.Reset()
		l.daprd.SignalHUP(t)
		l.daprd.WaitUntilRunning(t, ctx)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			l.daprd.HTTPGet2xx(c, ctx, "/v1.0/invoke/testapp/method/hi")
			assert.True(c, l.log.Contains("InvokeService/testapp"),
				"expected logs to contain obfuscated path 'InvokeService/testapp' when obfuscateURLs is true")
		}, 10*time.Second, 100*time.Millisecond)
	})
}
