/*
Copyright 2024 The Dapr Authors
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

package pathmatching

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(defaultCardinality))
}

// defaultCardinality tests daprd metrics for the HTTP server configured with path matching and default cardinality.
type defaultCardinality struct {
	daprd *daprd.Daprd
}

func (h *defaultCardinality) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/orders/{orderID}", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	h.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithInMemoryStateStore("mystore"),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pathMatching
spec:
  metrics:
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(app, h.daprd),
	}
}

func (h *defaultCardinality) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	t.Run("service invocation - default", func(t *testing.T) {
		h.daprd.HTTPGet2xx(t, ctx, "/v1.0/invoke/myapp/method/orders/123")
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := h.daprd.Metrics(t, ctx)
			assert.Equal(c, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:GET|path:/v1.0/invoke/myapp/method/orders/123|status:200"]))
		}, time.Second*10, time.Millisecond*10)
	})
}
