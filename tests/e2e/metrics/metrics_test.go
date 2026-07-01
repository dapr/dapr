//go:build e2e
// +build e2e

/*
Copyright 2021 The Dapr Authors
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

package metrics_e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type testCommandRequest struct {
	Message string `json:"message,omitempty"`
}

const numHealthChecks = 60 // Number of times to check for endpoint health per app.

const metricsWaitInterval = time.Second

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("metrics")
	utils.InitHTTPClient(false)

	// This test shows how to deploy the multiple test apps, validate the side-car injection
	// and validate the response by using test app's service endpoint

	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "httpmetrics",
			DaprEnabled:    true,
			ImageName:      "e2e-hellodapr",
			Config:         "metrics-config",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "grpcmetrics",
			DaprEnabled:    true,
			ImageName:      "e2e-stateapp",
			Config:         "metrics-config",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "disabledmetric",
			Config:         "disable-telemetry",
			DaprEnabled:    true,
			ImageName:      "e2e-hellodapr",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	tr = runner.NewTestRunner("metrics", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

type testCase struct {
	name          string
	app           string
	protocol      string
	action        func(t *testing.T, app string, n, port int)
	actionInvokes int
	evaluate      func(t *testing.T, app string, metricsPort int)
}

var metricsTests = []testCase{
	{
		"http metrics",
		"httpmetrics",
		"http",
		invokeDaprHTTP,
		3,
		testHTTPMetrics,
	},
	{
		"grpc metrics",
		"grpcmetrics",
		"grpc",
		invokeDaprGRPC,
		10,
		testGRPCMetrics,
	},
	{
		"metric off",
		"disabledmetric",
		"http",
		invokeDaprHTTP,
		3,
		testMetricDisabled,
	},
}

func TestMetrics(t *testing.T) {
	for _, tt := range metricsTests {
		// Open connection to the app on the dapr port and metrics port.
		// These will only be closed when the test runner is disposed after
		// all tests are run.
		var targetDaprPort int
		if tt.protocol == "http" {
			targetDaprPort = 3500
		} else if tt.protocol == "grpc" {
			targetDaprPort = 50001
		}
		localPorts, err := tr.Platform.PortForwardToApp(tt.app, targetDaprPort, 9090)
		require.NoError(t, err)

		// Port order is maintained when opening connection
		daprPort := localPorts[0]
		metricsPort := localPorts[1]

		t.Run(tt.name, func(t *testing.T) {
			// Perform an action n times using the Dapr API
			tt.action(t, tt.app, tt.actionInvokes, daprPort)

			// Evaluate the metrics are as expected
			tt.evaluate(t, tt.app, metricsPort)
		})
	}
}

func invokeDaprHTTP(t *testing.T, app string, n, daprPort int) {
	body, err := json.Marshal(testCommandRequest{
		Message: "Hello Dapr.",
	})
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		// We don't evaluate the response here as we're only testing the metrics
		_, err = utils.HTTPPost(fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/tests/green", daprPort, app), body)
		require.NoError(t, err)
	}
}

func testHTTPMetrics(t *testing.T, app string, metricsPort int) {
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics, err := findHTTPMetricFromPrometheus(app, metricsPort)
		assert.NoError(c, err)
		assert.True(c, metrics.foundMetric)
		assert.True(c, metrics.foundGet)
		assert.True(c, metrics.foundPost)
	}, numHealthChecks*metricsWaitInterval, metricsWaitInterval)
}

func testMetricDisabled(t *testing.T, app string, metricsPort int) {
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics, err := findHTTPMetricFromPrometheus(app, metricsPort)
		assert.NoError(c, err)
		assert.False(c, metrics.foundMetric)
	}, numHealthChecks*metricsWaitInterval, metricsWaitInterval)
}

type httpMetricStatus struct {
	foundMetric bool
	foundGet    bool
	foundPost   bool
}

func findHTTPMetricFromPrometheus(app string, metricsPort int) (httpMetricStatus, error) {
	status := httpMetricStatus{}

	mf, err := getMetricFamilyFromPrometheus(metricsPort, "dapr_http_server_request_count")
	if err != nil || mf == nil {
		return status, err
	}

	status.foundMetric = true

	for _, m := range mf.GetMetric() {
		if m == nil {
			continue
		}

		count := m.GetCounter()
		if count == nil || count.GetValue() == 0 {
			continue
		}

		var metricApp string
		var method string
		for _, l := range m.GetLabel() {
			if l == nil {
				continue
			}

			switch {
			case strings.EqualFold(l.GetName(), "app_id"):
				metricApp = l.GetValue()
			case strings.EqualFold(l.GetName(), "method"):
				method = l.GetValue()
			}
		}

		if !strings.EqualFold(metricApp, app) {
			continue
		}

		switch method {
		case http.MethodGet:
			status.foundGet = true
		case http.MethodPost:
			status.foundPost = true
		}
	}

	return status, nil
}

func invokeDaprGRPC(t *testing.T, app string, n, daprPort int) {
	daprAddress := fmt.Sprintf("localhost:%d", daprPort)
	conn, err := grpc.Dial(daprAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewDaprClient(conn)

	for i := 0; i < n; i++ {
		_, err = client.SaveState(t.Context(), &pb.SaveStateRequest{
			StoreName: "statestore",
			States: []*commonv1pb.StateItem{
				{
					Key:   "myKey",
					Value: []byte("My State"),
				},
			},
		})
		require.NoError(t, err)
	}
}

type grpcMetricStatus struct {
	foundMetric bool
	foundMethod bool
	count       int
}

func testGRPCMetrics(t *testing.T, app string, metricsPort int) {
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics, err := findGRPCMetricFromPrometheus(app, metricsPort)
		assert.NoError(c, err)
		assert.True(c, metrics.foundMetric)
		assert.True(c, metrics.foundMethod)
		assert.Equal(c, 10, metrics.count)
	}, numHealthChecks*metricsWaitInterval, metricsWaitInterval)
}

func findGRPCMetricFromPrometheus(app string, metricsPort int) (grpcMetricStatus, error) {
	status := grpcMetricStatus{}

	mf, err := getMetricFamilyFromPrometheus(metricsPort, "dapr_grpc_io_server_completed_rpcs")
	if err != nil || mf == nil {
		return status, err
	}

	status.foundMetric = true

	for _, m := range mf.GetMetric() {
		if m == nil {
			continue
		}

		var metricApp string
		var method string
		for _, l := range m.GetLabel() {
			if l == nil {
				continue
			}

			switch {
			case strings.EqualFold(l.GetName(), "app_id"):
				metricApp = l.GetValue()
			case strings.EqualFold(l.GetName(), "grpc_server_method"):
				method = l.GetValue()
			}
		}

		if !strings.EqualFold(metricApp, app) {
			continue
		}

		if strings.EqualFold(method, "/dapr.proto.runtime.v1.Dapr/SaveState") {
			status.foundMethod = true
			status.count = int(m.GetCounter().GetValue())
			break
		}
	}

	return status, nil
}

func getMetricFamilyFromPrometheus(metricsPort int, metricName string) (*io_prometheus_client.MetricFamily, error) {
	res, err := utils.HTTPGetRaw(fmt.Sprintf("http://localhost:%v", metricsPort))
	if err != nil {
		return nil, err
	}
	defer func() {
		// Drain before closing so the shared client can keep reusing the connection.
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()

	rfmt := expfmt.ResponseFormat(res.Header)
	if rfmt.FormatType() == expfmt.TypeUnknown {
		return nil, fmt.Errorf("unknown metrics response format")
	}

	decoder := expfmt.NewDecoder(res.Body, rfmt)

	for {
		mf := &io_prometheus_client.MetricFamily{}
		err = decoder.Decode(mf)
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}

		if strings.EqualFold(mf.GetName(), metricName) {
			return mf, nil
		}
	}
}
