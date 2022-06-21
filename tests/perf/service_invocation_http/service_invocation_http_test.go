//go:build perf
// +build perf

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

package service_invocation_http_perf

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const numHealthChecks = 60 // Number of times to check for endpoint health per app.

const testLabel = "service-invocation-http"

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("service_invocation_http")

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
			Labels: map[string]string{
				"daprtest": testLabel + "-testapp",
			},
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
			Labels: map[string]string{
				"daprtest": testLabel + "-tester",
			},
			PodAffinityLabels: map[string]string{
				"daprtest": testLabel + "-testapp",
			},
		},
	}

	tr = runner.NewTestRunner("serviceinvocationhttp", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestServiceInvocationHTTPPerformance(t *testing.T) {
	p := perf.Params()
	t.Logf("running service invocation http test with params: qps=%v, connections=%v, duration=%s, payload size=%v, payload=%v", p.QPS, p.ClientConnections, p.TestDuration, p.PayloadSizeKB, p.Payload)

	// Get the ingress external url of test app
	testAppURL := tr.Platform.AcquireAppExternalURL("testapp")
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("waiting until test app url is available: %s", testAppURL+"/test")
	_, err := utils.HTTPGetNTimes(testAppURL+"/test", numHealthChecks)
	require.NoError(t, err)

	// Get the ingress external url of tester app
	testerAppURL := tr.Platform.AcquireAppExternalURL("tester")
	require.NotEmpty(t, testerAppURL, "tester app external URL must not be empty")

	// Check if tester app endpoint is available
	t.Logf("waiting until tester app url is available: %s", testerAppURL)
	_, err = utils.HTTPGetNTimes(testerAppURL, numHealthChecks)
	require.NoError(t, err)

	// Perform baseline test
	endpoint := fmt.Sprintf("http://testapp:3000/test")
	p.TargetEndpoint = endpoint
	body, err := json.Marshal(&p)
	require.NoError(t, err)

	t.Log("running baseline test...")
	baselineResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
	t.Logf("baseline test results: %s", string(baselineResp))
	t.Log("checking err...")
	require.NoError(t, err)
	require.NotEmpty(t, baselineResp)
	// fast fail if daprResp starts with error
	require.False(t, strings.HasPrefix(string(baselineResp), "error"))

	// Perform dapr test
	endpoint = fmt.Sprintf("http://127.0.0.1:3500/v1.0/invoke/testapp/method/test")
	p.TargetEndpoint = endpoint
	body, err = json.Marshal(&p)
	require.NoError(t, err)

	t.Log("running dapr test...")
	daprResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
	t.Logf("dapr test results: %s", string(daprResp))
	t.Log("checking err...")
	require.NoError(t, err)
	require.NotEmpty(t, daprResp)
	// fast fail if daprResp starts with error
	require.False(t, strings.HasPrefix(string(daprResp), "error"))

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

	percentiles := map[int]string{1: "50th", 2: "75th", 3: "90th", 4: "99th"}
	tp90Latency := 0.0

	for k, v := range percentiles {
		daprValue := daprResult.DurationHistogram.Percentiles[k].Value
		baselineValue := baselineResult.DurationHistogram.Percentiles[k].Value

		latency := (daprValue - baselineValue) * 1000
		if v == "90th" {
			tp90Latency = latency
		}
		t.Logf("added latency for %s percentile: %sms", v, fmt.Sprintf("%.2f", latency))
	}
	avg := (daprResult.DurationHistogram.Avg - baselineResult.DurationHistogram.Avg) * 1000
	t.Logf("baseline latency avg: %sms", fmt.Sprintf("%.2f", baselineResult.DurationHistogram.Avg*1000))
	t.Logf("dapr latency avg: %sms", fmt.Sprintf("%.2f", daprResult.DurationHistogram.Avg*1000))
	t.Logf("added latency avg: %sms", fmt.Sprintf("%.2f", avg))

	report := perf.NewTestReport(
		[]perf.TestResult{baselineResult, daprResult},
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
	require.Greater(t, tp90Latency, 0.0)
	require.LessOrEqual(t, tp90Latency, 3.0)
}
