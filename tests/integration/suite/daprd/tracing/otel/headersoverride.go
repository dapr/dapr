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

package otel

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	httpClient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/otel"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(headersoverride))
}

// headersoverride tests that when both config file and env vars define OTel
// headers, the config file takes precedence.
type headersoverride struct {
	httpapp   *prochttp.HTTP
	daprd     *daprd.Daprd
	collector *otel.Collector
}

func (h *headersoverride) Setup(t *testing.T) []framework.Option {
	h.collector = otel.New(t)

	handler := http.NewServeMux()
	handler.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK`))
	})
	h.httpapp = prochttp.New(t, prochttp.WithHandler(handler))

	tracingConfig := fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: tracing
spec:
  tracing:
    samplingRate: "1.0"
    otel:
      endpointAddress: %s
      protocol: grpc
      isSecure: false
      headers:
        - "x-config-header=config-value"
      timeout: 30s
`, h.collector.OTLPGRPCAddress())

	h.daprd = daprd.New(t,
		daprd.WithAppID("test-otel-headers-override"),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(h.httpapp.Port()),
		daprd.WithConfigManifests(t, tracingConfig),
		daprd.WithExecOptions(
			exec.WithEnvVars(t,
				"OTEL_EXPORTER_OTLP_TRACES_HEADERS", "x-env-header=env-value",
				"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT", "5000",
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(h.collector, h.httpapp, h.daprd),
	}
}

func (h *headersoverride) Run(t *testing.T, ctx context.Context) {
	h.collector.WaitUntilRunning(t, ctx)
	h.daprd.WaitUntilRunning(t, ctx)
	client := httpClient.HTTP(t)

	t.Run("config headers take precedence over env vars", func(t *testing.T) {
		appURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/test", h.daprd.HTTPPort(), h.daprd.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, appURL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.NotEmpty(c, h.collector.GetSpans())
		}, time.Second*20, time.Millisecond*10, "should receive spans")

		md := h.collector.GetHeaders()
		assert.Equal(t, []string{"config-value"}, md.Get("x-config-header"))

		assert.Empty(t, md.Get("x-env-header"))
	})
}
