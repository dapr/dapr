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

package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

// Base struct for metrics tests
type base struct {
	daprd      *procdaprd.Daprd
	httpClient *http.Client
	grpcClient runtimev1pb.DaprClient
	grpcConn   *grpc.ClientConn
	place      *placement.Placement
}

func (m *base) testSetup(t *testing.T, daprdOpts ...procdaprd.Option) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/hi", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "OK")
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))
	m.place = placement.New(t)

	daprdOpts = append(daprdOpts,
		procdaprd.WithAppID("myapp"),
		procdaprd.WithAppPort(srv.Port()),
		procdaprd.WithAppProtocol("http"),
		procdaprd.WithPlacementAddresses(m.place.Address()),
		procdaprd.WithInMemoryActorStateStore("mystore"),
	)
	m.daprd = procdaprd.New(t, daprdOpts...)

	return []framework.Option{
		framework.WithProcesses(m.place, srv, m.daprd),
	}
}

func (m *base) beforeRun(t *testing.T, ctx context.Context) {
	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	m.httpClient = util.HTTPClient(t)

	var err error
	m.grpcConn, err = grpc.DialContext(ctx,
		m.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, m.grpcConn.Close()) })
	m.grpcClient = runtimev1pb.NewDaprClient(m.grpcConn)

	t.Logf("Metrics URL: http://localhost:%d/metrics", m.daprd.MetricsPort())
}

// Returns a subset of metrics scraped from the metrics endpoint
func (m *base) getMetrics(t *testing.T, ctx context.Context) map[string]float64 {
	t.Helper()

	reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
	defer reqCancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", m.daprd.MetricsPort()), nil)
	require.NoError(t, err)

	// Body is closed below but the linter isn't seeing that
	//nolint:bodyclose
	res, err := m.httpClient.Do(req)
	require.NoError(t, err)
	defer closeBody(t, res.Body)
	require.Equal(t, http.StatusOK, res.StatusCode)

	// Extract the metrics
	parser := expfmt.TextParser{}
	metricFamilies, err := parser.TextToMetricFamilies(res.Body)
	require.NoError(t, err)

	metrics := make(map[string]float64)
	for _, mf := range metricFamilies {
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

func (m *base) doRequest(t *testing.T, req *http.Request) {
	t.Helper()

	// Body is closed below but the linter isn't seeing that
	//nolint:bodyclose
	res, err := m.httpClient.Do(req)
	require.NoError(t, err)
	defer closeBody(t, res.Body)
	require.True(t, res.StatusCode >= 200 && res.StatusCode <= 299)
}

// Drain body before closing
func closeBody(t *testing.T, body io.ReadCloser) {
	_, err := io.Copy(io.Discard, body)
	require.NoError(t, err)
}
