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

package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appapitoken))
}

type appapitoken struct {
	daprd    *daprd.Daprd
	headerCh chan nethttp.Header
}

func (a *appapitoken) Setup(t *testing.T) []framework.Option {
	a.headerCh = make(chan nethttp.Header, 1)

	app := app.New(t,
		app.WithHandlerFunc("/mybinding", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			a.headerCh <- r.Header.Clone()
			w.WriteHeader(nethttp.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "SUCCESS"})
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppAPIToken(t, "test-binding-app-token"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mybinding
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
  - name: direction
    value: input
`))

	return []framework.Option{
		framework.WithProcesses(app, a.daprd),
	}
}

func (a *appapitoken) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	select {
	case headers := <-a.headerCh:
		token := headers.Get("dapr-api-token")
		assert.NotEmpty(t, token)
		assert.Equal(t, "test-binding-app-token", token)
	case <-time.After(time.Second * 10):
		assert.Fail(t, "Timed out waiting for binding event to be delivered to app")
	}
}
