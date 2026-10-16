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
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(tracingexport))
}

// tracingexport checks that spans are still exported after a SIGHUP restarts
// the runtime. The restart shuts the tracer provider down and installs a new
// one; spans started afterwards must be exported by the new provider.
type tracingexport struct {
	daprd     *daprd.Daprd
	collector *otel.Collector
}

func (te *tracingexport) Setup(t *testing.T) []framework.Option {
	te.collector = otel.New(t)

	config := fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: sighup-tracingexport
spec:
  tracing:
    samplingRate: "1"
    otel:
      endpointAddress: %q
      protocol: grpc
      isSecure: false
`, te.collector.OTLPGRPCAddress())

	testApp := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	te.daprd = daprd.New(t,
		daprd.WithAppPort(testApp.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("testapp"),
		daprd.WithConfigs(os.WriteFileYaml(t, config)),
	)

	return []framework.Option{
		framework.WithProcesses(te.collector, testApp, te.daprd),
	}
}

func (te *tracingexport) Run(t *testing.T, ctx context.Context) {
	te.collector.WaitUntilRunning(t, ctx)
	te.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	invoke := func(t *testing.T, traceID trace.TraceID) {
		t.Helper()

		url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/hi", te.daprd.HTTPAddress(), te.daprd.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)
		req.Header.Set("traceparent", fmt.Sprintf("00-%s-00f067aa0ba902b7-01", traceID))

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	requireExported := func(t *testing.T, traceID trace.TraceID) {
		t.Helper()

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NotEmpty(c, te.collector.TraceSpans(traceID),
				"expected the collector to receive spans of trace %s", traceID)
		}, 20*time.Second, 100*time.Millisecond)
	}

	t.Run("spans are exported before SIGHUP", func(t *testing.T) {
		traceID := trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36}
		invoke(t, traceID)
		requireExported(t, traceID)
	})

	t.Run("spans are exported after SIGHUP", func(t *testing.T) {
		te.daprd.SignalHUP(t)
		te.daprd.WaitUntilRunning(t, ctx)

		traceID := trace.TraceID{0x5b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x37}
		invoke(t, traceID)
		requireExported(t, traceID)
	})
}
