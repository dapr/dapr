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
	"net/http"
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
	suite.Register(new(highPathMatching))
}

// highPathMatching tests daprd metrics for the HTTP server configured with high cardinality AND path matching
type highPathMatching struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (h *highPathMatching) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		}),
		app.WithHandlerFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"entities": ["myactortype"]}`))
		}),
	)
	h.place = placement.New(t)

	h.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithPlacementAddresses(h.place.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: highcardinalitypathmatching
spec:
  metrics:
    http:
      increasedCardinality: true
      pathMatching:
        - /v1.0/actors/{actorType}/{actorId}/method/{method}
`),
	)
	return []framework.Option{
		framework.WithProcesses(app, h.daprd, h.place),
	}
}

func (h *highPathMatching) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)
	h.place.WaitUntilRunning(t, ctx)

	t.Run("actor invocation matched", func(t *testing.T) {
		h.daprd.HTTPPost2xx(t, ctx, "/v1.0/actors/myactortype/myactorid/method/foo", nil, "content-type", "application/json")
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := h.daprd.Metrics(c, ctx).All()
			// Should match the specific path matching rule
			assert.Equal(c, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:POST|path:/v1.0/actors/{actorType}/{actorId}/method/{method}|status:200"]))
		}, time.Second*10, 10*time.Millisecond)
	})

	t.Run("unmatched path raw preservation", func(t *testing.T) {
		// Request a path that is NOT in pathMatching
		h.daprd.HTTPGet2xx(t, ctx, "/v1.0/metadata")
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := h.daprd.Metrics(c, ctx).All()
			// Expect raw path
			assert.Equal(c, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:GET|path:/v1.0/metadata|status:200"]))
		}, time.Second*10, 10*time.Millisecond)
	})
}
