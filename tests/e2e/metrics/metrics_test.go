// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package metrics_e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/require"
)

type testCommandRequest struct {
	Message string `json:"message,omitempty"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

const numHealthChecks = 60 // Number of times to check for endpoint health per app.

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// This test shows how to deploy the multiple test apps, validate the side-car injection
	// and validate the response by using test app's service endpoint

	// These apps will be deployed for hellodapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "httpmetrics",
			DaprEnabled:    true,
			ImageName:      "e2e-hellodapr",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner("metrics", testApps, nil)
	os.Exit(tr.Start(m))
}

var metricsTests = []struct {
	in            string
	app           string
	testCommand   string
	numCalls      int
	checkResponse func(t *testing.T, res *http.Response)
}{
	{
		"http metrics",
		"httpmetrics",
		"green",
		3,
		func(t *testing.T, res *http.Response) {
			require.NotNil(t, res)

			fmt := expfmt.ResponseFormat(res.Header)
			require.NotEqual(t, fmt, expfmt.FmtUnknown)

			decoder := expfmt.NewDecoder(res.Body, fmt)
			var foundPath bool
			for {
				mf := &io_prometheus_client.MetricFamily{}
				err := decoder.Decode(mf)
				if err == io.EOF {
					break
				}
				require.NoError(t, err)

				if strings.EqualFold(mf.GetName(), "http_handler_completed_requests_total") {
					for _, m := range mf.GetMetric() {
						if m == nil {
							continue
						}
						for _, l := range m.GetLabel() {
							if l == nil {
								continue
							}
							if strings.EqualFold(l.GetName(), "path") {
								foundPath = true
								require.Equal(t, "/v1.0/invoke/httpmetrics/method/tests/green", l.GetValue())
							}
						}
						require.Equal(t, 3, int(m.GetCounter().GetValue()))
					}
				}
			}
			require.True(t, foundPath)
		},
	},
}

func TestMetrics(t *testing.T) {
	for _, tt := range metricsTests {
		t.Run(tt.in, func(t *testing.T) {
			// Get the ingress external url of test app
			externalURL := tr.Platform.AcquireAppExternalURL(tt.app)
			require.NotEmpty(t, externalURL, "external URL must not be empty")

			invokeTestWithDapr(t, tt.numCalls, tt.app, tt.testCommand)

			port, err := tr.Platform.OpenConnection(tt.app, 9090)
			require.NoError(t, err)

			defer func() {
				err := tr.Platform.CloseConnection(tt.app, 9090)
				require.NoError(t, err)
			}()

			// Check if metrics endpoint is available
			res, err := utils.HTTPGetRawNTimes(fmt.Sprintf("http://localhost:%v", port), numHealthChecks)
			require.NoError(t, err)

			tt.checkResponse(t, res)
		})
	}
}

func invokeTestWithDapr(t *testing.T, n int, app, method string) {
	body, err := json.Marshal(testCommandRequest{
		Message: "Hello Dapr.",
	})
	require.NoError(t, err)

	port, err := tr.Platform.OpenConnection(app, 3500)
	require.NoError(t, err)

	defer func() {
		err := tr.Platform.CloseConnection(app, 3500)
		require.NoError(t, err)
	}()

	for i := 0; i < n; i++ {
		// We don't evaluate the response here as we're only testing the metrics
		_, err = utils.HTTPPost(fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/tests/%s", port, app, method), body)
		require.NoError(t, err)
	}
}
