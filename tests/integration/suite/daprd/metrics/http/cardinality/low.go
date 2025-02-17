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

package cardinality

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(low))
}

// low tests daprd metrics for the HTTP server configured with low cardinality
type low struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (l *low) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
		app.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		}),
	)

	l.place = placement.New(t)

	l.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithPlacementAddresses(l.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: lowcardinality
spec:
  metrics:
    http:
      increasedCardinality: false
`),
	)

	return []framework.Option{
		framework.WithProcesses(app, l.daprd, l.place),
	}
}

func (l *low) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)
	l.place.WaitUntilRunning(t, ctx)

	t.Run("service invocation", func(t *testing.T) {
		l.daprd.HTTPGet2xx(t, ctx, "/v1.0/invoke/myapp/method/hi")
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := l.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:GET|path:|status:200"]))
			assert.Equal(c, 1, int(metrics["dapr_http_client_completed_count|app_id:myapp|method:GET|path:/dapr/config|status:200"]))
			assert.NotContains(c, metrics, "dapr_http_server_response_count|app_id:myapp|method:GET|path:/v1.0/invoke/myapp/method/hi|status:200")
			assert.NotContains(c, metrics, "dapr_http_server_response_count|app_id:myapp|method:GET|path:/v1.0/healthz|status:204 1.000000")
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("state stores", func(t *testing.T) {
		body := `[{"key":"myvalue", "value":"hello world"}]`
		l.daprd.HTTPPost2xx(t, ctx, "/v1.0/state/mystore", strings.NewReader(body), "content-type", "application/json")
		l.daprd.HTTPGet2xx(t, ctx, "/v1.0/state/mystore/myvalue")
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := l.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:POST|path:|status:204"]))
			assert.Equal(c, 2, int(metrics["dapr_http_server_request_count|app_id:myapp|method:GET|path:|status:200"]))
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("actor invocation", func(t *testing.T) {
		l.daprd.HTTPPost2xx(t, ctx, "/v1.0/actors/myactortype/myactorid/method/foo", nil, "content-type", "application/json")
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := l.daprd.Metrics(c, ctx).All()
			assert.Equal(c, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:POST|path:|status:200"]))
			assert.NotContains(c, metrics, "method:InvokeActor/myactortype.")
		}, time.Second*5, time.Millisecond*10)
	})
}
