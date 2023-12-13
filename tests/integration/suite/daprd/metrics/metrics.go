/*
Copyright 2023 The Dapr Authors
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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	prometheus "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(metrics))
}

// metrics tests daprd metrics
type metrics struct {
	daprd      *procdaprd.Daprd
	httpClient *http.Client
	grpcClient runtimev1pb.DaprClient
}

func (m *metrics) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/hi", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "OK")
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	m.daprd = procdaprd.New(t,
		procdaprd.WithAppID("myapp"),
		procdaprd.WithAppPort(srv.Port()),
		procdaprd.WithAppProtocol("http"),
		procdaprd.WithInMemoryActorStateStore("mystore"),
	)

	return []framework.Option{
		framework.WithProcesses(srv, m.daprd),
	}
}

func (m *metrics) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	m.httpClient = util.HTTPClient(t)

	conn, err := grpc.DialContext(ctx,
		m.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	m.grpcClient = runtimev1pb.NewDaprClient(conn)

	t.Logf("Metrics URL: http://localhost:%d/metrics", m.daprd.MetricsPort())

	t.Run("HTTP", func(t *testing.T) {
		t.Run("service invocation", func(t *testing.T) {
			reqCtx, reqCancel := context.WithTimeout(ctx, time.Second)
			defer reqCancel()

			// Invoke
			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/invoke/myapp/method/hi", m.daprd.HTTPPort()), nil)
			require.NoError(t, err)
			m.doRequest(t, req)

			// Verify metrics
			metrics := m.getMetrics(t, ctx)
			assert.Equal(t, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:InvokeService/myapp|status:200"]))
		})

		t.Run("state stores", func(t *testing.T) {
			reqCtx, reqCancel := context.WithTimeout(ctx, time.Second)
			defer reqCancel()

			// Write state
			body := `[{"key":"myvalue", "value":"hello world"}]`
			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, fmt.Sprintf("http://localhost:%d/v1.0/state/mystore", m.daprd.HTTPPort()), strings.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("content-type", "application/json")
			m.doRequest(t, req)

			// Get state
			req, err = http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/state/mystore/myvalue", m.daprd.HTTPPort()), nil)
			require.NoError(t, err)
			m.doRequest(t, req)

			// Verify metrics
			metrics := m.getMetrics(t, ctx)
			assert.Equal(t, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:SaveState|status:204"]))
			assert.Equal(t, 1, int(metrics["dapr_http_server_request_count|app_id:myapp|method:GetState|status:200"]))
		})
	})

	t.Run("gRPC", func(t *testing.T) {
		t.Run("service invocation", func(t *testing.T) {
			reqCtx, reqCancel := context.WithTimeout(ctx, time.Second)
			defer reqCancel()

			// Invoke
			_, err := m.grpcClient.InvokeService(reqCtx, &runtimev1pb.InvokeServiceRequest{
				Id: "myapp",
				Message: &commonv1pb.InvokeRequest{
					Method: "hi",
					HttpExtension: &commonv1pb.HTTPExtension{
						Verb: commonv1pb.HTTPExtension_GET,
					},
				},
			})
			require.NoError(t, err)

			// Verify metrics
			metrics := m.getMetrics(t, ctx)
			assert.Equal(t, 1, int(metrics["dapr_grpc_io_server_completed_rpcs|app_id:myapp|grpc_server_method:/dapr.proto.runtime.v1.Dapr/InvokeService|grpc_server_status:OK"]))
		})

		t.Run("state stores", func(t *testing.T) {
			reqCtx, reqCancel := context.WithTimeout(ctx, time.Second)
			defer reqCancel()

			// Write state
			_, err := m.grpcClient.SaveState(reqCtx, &runtimev1pb.SaveStateRequest{
				StoreName: "mystore",
				States: []*commonv1pb.StateItem{
					{Key: "myvalue", Value: []byte(`"hello world"`)},
				},
			})
			require.NoError(t, err)

			// Get state
			_, err = m.grpcClient.GetState(reqCtx, &runtimev1pb.GetStateRequest{
				StoreName: "mystore",
				Key:       "myvalue",
			})
			require.NoError(t, err)

			// Verify metrics
			metrics := m.getMetrics(t, ctx)
			assert.Equal(t, 1, int(metrics["dapr_grpc_io_server_completed_rpcs|app_id:myapp|grpc_server_method:/dapr.proto.runtime.v1.Dapr/SaveState|grpc_server_status:OK"]))
			assert.Equal(t, 1, int(metrics["dapr_grpc_io_server_completed_rpcs|app_id:myapp|grpc_server_method:/dapr.proto.runtime.v1.Dapr/GetState|grpc_server_status:OK"]))
		})
	})
}

// Returns a subset of metrics scraped from the metrics endpoint
func (m *metrics) getMetrics(t *testing.T, ctx context.Context) map[string]float64 {
	t.Helper()

	reqCtx, reqCancel := context.WithTimeout(ctx, time.Second)
	defer reqCancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", m.daprd.MetricsPort()), nil)
	require.NoError(t, err)

	// Body is closed below but the linter isn't seeing that
	//nolint:bodyclose
	res, err := m.httpClient.Do(req)
	require.NoError(t, err)
	defer closeBody(res.Body)
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Extract the metrics
	parser := expfmt.TextParser{}
	metricFamilies, err := parser.TextToMetricFamilies(res.Body)
	require.NoError(t, err)

	metrics := make(map[string]float64)
	for _, mf := range metricFamilies {
		if mf.GetType() != prometheus.MetricType_COUNTER {
			continue
		}

		for _, m := range mf.GetMetric() {
			key := mf.GetName()
			for _, l := range m.GetLabel() {
				key += "|" + l.GetName() + ":" + l.GetValue()
			}
			metrics[key] = m.GetCounter().GetValue()
		}
	}

	return metrics
}

func (m *metrics) doRequest(t *testing.T, req *http.Request) {
	t.Helper()

	// Body is closed below but the linter isn't seeing that
	//nolint:bodyclose
	res, err := m.httpClient.Do(req)
	require.NoError(t, err)
	defer closeBody(res.Body)
	require.True(t, res.StatusCode >= 200 && res.StatusCode <= 299)
}

// Drain body before closing
func closeBody(body io.ReadCloser) error {
	_, err := io.Copy(io.Discard, body)
	if err != nil {
		return err
	}
	return body.Close()
}
