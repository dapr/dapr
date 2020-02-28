// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package metrics_e2e

import (
	"context"
	"crypto/tls"
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
	pb "github.com/dapr/go-sdk/dapr"
	"github.com/golang/protobuf/ptypes/any"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type testCommandRequest struct {
	Message string `json:"message,omitempty"`
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
		{
			AppName:        "grpcmetrics",
			DaprEnabled:    true,
			ImageName:      "e2e-stateapp",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	tr = runner.NewTestRunner("metrics", testApps, nil)
	os.Exit(tr.Start(m))
}

type testCase struct {
	name          string
	app           string
	protocol      string
	action        func(t *testing.T, app string, n, port int)
	actionInvokes int
	evaluate      func(t *testing.T, app string, res *http.Response)
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
		localPorts, err := tr.Platform.OpenConnection(tt.app, targetDaprPort, 9090)
		require.NoError(t, err)

		// Port order is maintained when opening connection
		daprPort := localPorts[0]
		metricsPort := localPorts[1]

		t.Run(tt.name, func(t *testing.T) {
			// Perform an action n times using the Dapr API
			tt.action(t, tt.app, tt.actionInvokes, daprPort)

			// Get the metrics from the metrics endpoint
			res, err := utils.HTTPGetRawNTimes(fmt.Sprintf("http://localhost:%v", metricsPort), numHealthChecks)
			require.NoError(t, err)

			// Evaluate the metrics are as expected
			tt.evaluate(t, tt.app, res)
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

func testHTTPMetrics(t *testing.T, app string, res *http.Response) {
	require.NotNil(t, res)

	rfmt := expfmt.ResponseFormat(res.Header)
	require.NotEqual(t, rfmt, expfmt.FmtUnknown)

	decoder := expfmt.NewDecoder(res.Body, rfmt)

	// This test will loop through each of the metrics and look for a specifc
	// metric `http_handler_completed_requests_total`. Once it finds the metric
	// it will check the `path` label is as expected for the invoked action.
	var foundMetric bool
	var foundPath bool
	for {
		mf := &io_prometheus_client.MetricFamily{}
		err := decoder.Decode(mf)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		if strings.EqualFold(mf.GetName(), "http_handler_completed_requests_total") {
			foundMetric = true
			for _, m := range mf.GetMetric() {
				if m == nil {
					continue
				}
				// check metrics with expected method exists
				for _, l := range m.GetLabel() {
					if l == nil {
						continue
					}
					if strings.EqualFold(l.GetName(), "path") {
						foundPath = true

						require.Equal(t, fmt.Sprintf("/v1.0/invoke/%s/method/tests/green", app), l.GetValue())
						break
					}
				}
			}
		}
	}
	// Check metric was found
	require.True(t, foundMetric)
	// Check metric with method was found
	require.True(t, foundPath)
}

func invokeDaprGRPC(t *testing.T, app string, n, daprPort int) {
	daprAddress := fmt.Sprintf("localhost:%d", daprPort)
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}
	creds := credentials.NewTLS(tlsConfig)
	conn, err := grpc.Dial(daprAddress, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewDaprClient(conn)

	for i := 0; i < n; i++ {
		_, err = client.SaveState(context.Background(), &pb.SaveStateEnvelope{
			StoreName: "statestore",
			Requests: []*pb.StateRequest{
				&pb.StateRequest{
					Key: "myKey",
					Value: &any.Any{
						Value: []byte("My State"),
					},
				},
			},
		})
		require.NoError(t, err)
	}
}

func testGRPCMetrics(t *testing.T, app string, res *http.Response) {
	require.NotNil(t, res)

	rfmt := expfmt.ResponseFormat(res.Header)
	require.NotEqual(t, rfmt, expfmt.FmtUnknown)

	decoder := expfmt.NewDecoder(res.Body, rfmt)

	// This test will loop through each of the metrics and look for a specifc
	// metric `grpc_server_msg_received_total`. This metric will exist for
	// multiple `grpc_method` labels, therefore, we loop through the labels
	// to find the the instance that has `grpc=method="SaveState". Once we
	// find the desired metric entry, we check the metric's value is as expected.`
	var foundMetric bool
	var foundMethod bool
	for {
		mf := &io_prometheus_client.MetricFamily{}
		err := decoder.Decode(mf)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		if strings.EqualFold(mf.GetName(), "grpc_server_msg_received_total") {
			foundMetric = true
			for _, m := range mf.GetMetric() {
				if m == nil {
					continue
				}
				// Check path label is as expected
				for _, l := range m.GetLabel() {
					if l == nil {
						continue
					}

					if strings.EqualFold(l.GetName(), "grpc_method") {
						if strings.EqualFold(l.GetValue(), "SaveState") {
							foundMethod = true

							// Check value is as expected
							require.Equal(t, 10, int(m.GetCounter().GetValue()))
							break
						}
					}
				}
			}
		}
	}
	// Check metric was found
	require.True(t, foundMetric)
	// Check path label was found
	require.True(t, foundMethod)
}
