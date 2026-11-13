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

package metrics

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
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disabled))
}

// disabled tests that metric.enabled false is not re-enabled by
// metrics.enabled true, which the Kubernetes CRD sets by default.
type disabled struct {
	daprd *daprd.Daprd
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	app := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	d.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: metricsdisabled
spec:
  metric:
    enabled: false
  metrics:
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(app, d.daprd),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	d.daprd.HTTPGet2xx(t, ctx, "/v1.0/invoke/myapp/method/hi")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := d.daprd.Metrics(c, ctx).All()
		assert.Contains(c, metrics, "go_goroutines")
		var recorded []string
		for name := range metrics {
			if strings.HasPrefix(name, "dapr_") {
				recorded = append(recorded, name)
			}
		}
		assert.Empty(c, recorded)
	}, time.Second*5, time.Millisecond*10)
}
