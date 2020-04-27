// +build perf

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package service_invocation_http_perf

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
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
			AppName:        "testapp",
			DaprEnabled:    true,
			ImageName:      "perf-service_invocation_http",
			Replicas:       1,
			IngressEnabled: true,
		},
		{
			AppName:        "tester",
			DaprEnabled:    true,
			ImageName:      "perf-tester",
			Replicas:       1,
			IngressEnabled: true,
			AppPort:        3001,
		},
	}

	tr = runner.NewTestRunner("serviceinvocationhttp", testApps, nil)
	os.Exit(tr.Start(m))
}

func TestServiceInvocationHTTPPerformance(t *testing.T) {
	p := perf.ParamsFromDefaults()

	if val, ok := os.LookupEnv(perf.ClientConnectionsEnvVar); ok && val != "" {
		clientConn, err := strconv.Atoi(val)
		require.NoError(t, err)
		p.ClientConnections = clientConn
	}
	if val, ok := os.LookupEnv(perf.TestDurationEnvVar); ok && val != "" {
		p.TestDuration = val
	}
	if val, ok := os.LookupEnv(perf.PayloadSizeEnvVar); ok && val != "" {
		payloadSize, err := strconv.Atoi(val)
		require.NoError(t, err)
		p.PayloadSizeKB = payloadSize
	}
	if val, ok := os.LookupEnv(perf.QPSEnvVar); ok && val != "" {
		qps, err := strconv.Atoi(val)
		require.NoError(t, err)
		p.QPS = qps
	}
	t.Logf("running service invocation http test with params: qps=%v, connections=%v, duration=%s, payload size=%v", p.QPS, p.ClientConnections, p.TestDuration, p.PayloadSizeKB)

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
	t.Logf("teter app url: %s", testerAppURL)
	_, err = utils.HTTPGetNTimes(testerAppURL, numHealthChecks)
	require.NoError(t, err)

	// Perform baseline test
	endpoint := fmt.Sprintf("http://testapp:3000/test")
	p.TargetEndpoint = endpoint
	body, err := json.Marshal(&p)
	require.NoError(t, err)

	t.Log("runnng baseline test...")
	baselineResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
	t.Log("checking err...")
	require.NoError(t, err)
	require.NotEmpty(t, baselineResp)

	t.Logf("baseline test results: %s", string(baselineResp))

	// Perform dapr test
	endpoint = fmt.Sprintf("http://127.0.0.1:3500/v1.0/invoke/testapp/method/test")
	p.TargetEndpoint = endpoint
	body, err = json.Marshal(&p)
	require.NoError(t, err)

	t.Log("running dapr test...")
	daprResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
	t.Log("checking err...")
	require.NoError(t, err)
	require.NotEmpty(t, daprResp)

	t.Logf("dapr test results: %s", string(daprResp))

	var daprResult perf.TestResult
	err = json.Unmarshal(daprResp, &daprResult)
	require.NoError(t, err)

	var baselineResult perf.TestResult
	err = json.Unmarshal(baselineResp, &baselineResult)
	require.NoError(t, err)

	daprValue := daprResult.DurationHistogram.Percentiles[1].Value
	baselineValue := baselineResult.DurationHistogram.Percentiles[1].Value

	latency := (daprValue - baselineValue) * 1000
	t.Logf("added latency for 75th percentile: %sms", fmt.Sprintf("%.2f", latency))
}
