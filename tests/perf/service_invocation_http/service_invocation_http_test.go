// +build perf

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package service_invocation_http_perf

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const numHealthChecks = 60 // Number of times to check for endpoint health per app.

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	testApps := []kube.AppDescription{
		{
			AppName:           "testapp",
			DaprEnabled:       true,
			ImageName:         "perf-service_invocation_http",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "512Mi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
		},
		{
			AppName:           "tester",
			DaprEnabled:       true,
			ImageName:         "perf-tester",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			AppPort:           3001,
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "512Mi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
		},
	}

	// If running cross network tests, add gateway config to apps.
	if os.Getenv("DAPR_XNET_RUN") != "" {
		testApps[0].Config = "gateway-config"
		testApps[1].Config = "gateway-config"
	}

	tr = runner.NewTestRunner("serviceinvocationhttp", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestServiceInvocationHTTPPerformance(t *testing.T) {
	p := perf.Params()

	var xNetLog string
	if p.RunCrossNetworkTests {
		if p.CrossNetworkAppID == "" || p.CrossNetworkBaselineEndpoint == "" {
			t.Error("cross-network baseline endpoint and cross-network app id must be set when cross-network tests are enabled.")
			return
		}
		xNetLog = fmt.Sprintf(", cross-network test=enabled, cross-network baseline address=%s, cross-network app id=%s", p.CrossNetworkBaselineEndpoint, p.CrossNetworkAppID)
	}
	t.Logf("running service invocation http test with params: qps=%v, connections=%v, duration=%s, payload size=%v, payload=%v %s", p.QPS, p.ClientConnections, p.TestDuration, p.PayloadSizeKB, p.Payload, xNetLog)

	// Get the ingress external url of test app
	testAppURL := tr.Platform.AcquireAppExternalURL("testapp")
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/test")
	_, err := utils.HTTPGetNTimes(testAppURL+"/test", numHealthChecks)
	require.NoError(t, err)

	// Get the ingress external url of tester app
	testerAppURL := tr.Platform.AcquireAppExternalURL("tester")
	require.NotEmpty(t, testerAppURL, "tester app external URL must not be empty")

	// Check if tester app endpoint is available
	t.Logf("tester app url: %s", testerAppURL)
	_, err = utils.HTTPGetNTimes(testerAppURL, numHealthChecks)
	require.NoError(t, err)

	// Perform baseline test
	testID := "baseline"
	endpoint := fmt.Sprintf("http://testapp:3000/test")
	p.TargetEndpoint = endpoint
	baselineResp := runTestCase(t, testID, testerAppURL, &p)
	t.Logf("%s test results: %s", testID, string(baselineResp))

	// Perform dapr test
	testID = "dapr"
	endpoint = fmt.Sprintf("http://127.0.0.1:3500/v1.0/invoke/testapp/method/test")
	p.TargetEndpoint = endpoint
	daprResp := runTestCase(t, testID, testerAppURL, &p)
	t.Logf("%s test results: %s", testID, string(daprResp))

	var crossNetworkBaselineResp []byte
	var crossNetworkDaprResp []byte
	if p.RunCrossNetworkTests {
		// Perform baseline cross-network test
		testID = "cross-network baseline"
		endpoint = fmt.Sprintf("http://%s/test", p.CrossNetworkBaselineEndpoint)
		p.TargetEndpoint = endpoint
		crossNetworkBaselineResp = runTestCase(t, testID, testerAppURL, &p)
		t.Logf("%s test results: %s", testID, string(crossNetworkBaselineResp))

		// Perform cross-network test
		testID = "cross-network dapr"
		endpoint = fmt.Sprintf("http://127.0.0.1:3500/v1.0/invoke/%s/method/test", p.CrossNetworkAppID)
		p.TargetEndpoint = endpoint
		crossNetworkDaprResp = runTestCase(t, testID, testerAppURL, &p)
		t.Logf("%s test results: %s", testID, string(crossNetworkDaprResp))
	}

	sidecarUsage, err := tr.Platform.GetSidecarUsage("testapp")
	require.NoError(t, err)

	appUsage, err := tr.Platform.GetAppUsage("testapp")
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts("testapp")
	require.NoError(t, err)

	t.Logf("target dapr sidecar consumed %vm Cpu and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)

	var daprResult perf.TestResult
	err = json.Unmarshal(daprResp, &daprResult)
	require.NoError(t, err)

	var baselineResult perf.TestResult
	err = json.Unmarshal(baselineResp, &baselineResult)
	require.NoError(t, err)

	var crossNetworkBaselineResult perf.TestResult
	var crossNetworkDaprResult perf.TestResult
	if p.RunCrossNetworkTests {
		err = json.Unmarshal(crossNetworkBaselineResp, &crossNetworkBaselineResult)
		require.NoError(t, err)

		err = json.Unmarshal(crossNetworkDaprResp, &crossNetworkDaprResult)
		require.NoError(t, err)
	}

	percentiles := map[int]string{1: "75th", 2: "90th"}

	for k, v := range percentiles {
		daprValue := daprResult.DurationHistogram.Percentiles[k].Value
		baselineValue := baselineResult.DurationHistogram.Percentiles[k].Value

		latencyDiff := (daprValue - baselineValue) * 1000
		logPercentileLatency(t, v, daprValue, latencyDiff, "latency added by dapr over baseline performance.")

		if p.RunCrossNetworkTests {
			crossNetworkBaselineValue := crossNetworkBaselineResult.DurationHistogram.Percentiles[k].Value
			crossNetworkDaprValue := crossNetworkDaprResult.DurationHistogram.Percentiles[k].Value

			crossNetworkDaprLatency := (crossNetworkDaprValue - crossNetworkBaselineValue) * 1000
			logPercentileLatency(t, v, crossNetworkDaprValue, crossNetworkDaprLatency, "latency added by dapr over cross-network baseline performance.")
		}
	}

	results := []perf.TestResult{baselineResult, daprResult}
	if p.RunCrossNetworkTests {
		results = append(results, crossNetworkBaselineResult, crossNetworkDaprResult)
	}

	report := perf.NewTestReport(
		results,
		"Service Invocation",
		sidecarUsage,
		appUsage)
	report.SetTotalRestartCount(restarts)
	err = utils.UploadAzureBlob(report)

	if err != nil {
		t.Error(err)
	}

	require.Equal(t, 0, daprResult.RetCodes.Num400)
	require.Equal(t, 0, daprResult.RetCodes.Num500)
	require.Equal(t, 0, restarts)
	require.True(t, daprResult.ActualQPS > float64(p.QPS)*0.99)
}

func logPercentileLatency(t *testing.T, percentile string, latency, baselineDiff float64, reason string) {
	t.Logf("percentile=%s, latency=%sms, baselineDiff=%sms, reason=%s", percentile, fmt.Sprintf("%.2f", latency), fmt.Sprintf("%.2f", baselineDiff), reason)
}

func runTestCase(t *testing.T, id string, testerAppURL string, testParams *perf.TestParameters) []byte {
	body, err := json.Marshal(&testParams)
	require.NoError(t, err)

	t.Logf("running %s test...", id)
	resp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
	t.Logf("checking %s test err...", id)
	require.NoError(t, err)
	require.NotEmpty(t, resp)

	return resp
}
