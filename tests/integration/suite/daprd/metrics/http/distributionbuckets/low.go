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

package distributionbuckets

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(low))
}

// low tests daprd metrics for the HTTP server configured with low cardinality
// of latency distribution buckets
type low struct {
	daprd *daprd.Daprd
}

func (l *low) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	l.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithInMemoryStateStore("mystore"),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: lowcardinality
spec:
  metrics:
    http:
      increasedCardinality: false
      latencyDistributionBuckets:
        - 5
        - 50
        - 500
        - 5000
`),
	)

	return []framework.Option{
		framework.WithProcesses(app, l.daprd),
	}
}

func (l *low) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)

	t.Run("service invocation", func(t *testing.T) {
		l.daprd.HTTPGet2xx(t, ctx, "/v1.0/invoke/myapp/method/hi")
		metrics := l.daprd.Metrics(t, ctx)
		httpServerLatencyBuckets := 0
		for k := range metrics {
			fmt.Println(k)
			if strings.HasPrefix(k, "dapr_http_server_latency_bucket") && strings.Contains(k, "app_id:myapp") && strings.Contains(k, "status:200") {
				httpServerLatencyBuckets += 1
			}
		}
		assert.Equal(t, 5, httpServerLatencyBuckets)
	})
}
