//go:build perf
// +build perf

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

package configuration

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/loadtest"
	"github.com/dapr/dapr/tests/runner/summary"
	"github.com/stretchr/testify/require"
)

var tr *runner.TestRunner

const (
	numHealthChecks                       = 60 // Number of times to check for endpoint health per app.
	defaultConfigGetThresholdMs           = 60
	defaultConfigSubscribeHTTPThresholdMs = 450 // HTTP subscribe makes per-key HTTP round-trips, resulting in higher latency than GRPC streaming
	defaultConfigSubscribeGRPCThresholdMs = 350 // GRPC subscribe uses streaming
	testAppName                           = "configurationapp"
)

type Item struct {
	Value    string            `json:"value,omitempty"`
	Version  string            `json:"version,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

func TestMain(m *testing.M) {
	utils.SetupLogs("configuration_test")

	testApps := []kube.AppDescription{
		{
			AppName:           testAppName,
			DaprEnabled:       true,
			ImageName:         "perf-configuration",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "200Mi",
			DaprMemoryRequest: "100Mi",
			AppMemoryLimit:    "200Mi",
			AppMemoryRequest:  "100Mi",
		},
	}

	tr = runner.NewTestRunner("configuration_http_test", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func runk6test(t *testing.T, targetURL string, payload []byte, threshold int) *loadtest.K6RunnerMetricsSummary {
	k6Test := loadtest.NewK6(
		"./test.js",
		loadtest.WithParallelism(1),
		// loadtest.EnableLog(), // uncomment this to enable k6 logs, this however breaks reporting, only for debugging.
		loadtest.WithRunnerEnvVar("TARGET_URL", targetURL),
		loadtest.WithRunnerEnvVar("PAYLOAD", string(payload)),
		loadtest.WithRunnerEnvVar("HTTP_REQ_DURATION_THRESHOLD", strconv.Itoa(threshold)),
	)
	defer k6Test.Dispose()
	t.Log("running the k6 load test...")
	require.NoError(t, tr.Platform.LoadTest(k6Test))
	sm, err := loadtest.K6ResultDefault(k6Test)
	require.NoError(t, err)
	require.NotNil(t, sm)
	bts, err := json.MarshalIndent(sm, "", " ")
	require.NoError(t, err)
	require.True(t, sm.Pass, fmt.Sprintf("test has not passed, results %s", string(bts)))
	utils.LogPerfTestSummary(bts)
	return sm.RunnersResults[0]
}

func subscribeTest(t *testing.T, externalURL string, test string, protocol string) *loadtest.K6RunnerMetricsSummary {
	var appResp appResponse
	items := map[string]*Item{
		"key1": {
			Value: "val1",
		},
	}

	// Subscribe to key `key1` in config store
	subscribeURL := fmt.Sprintf("http://%s/subscribe/%s/%s", externalURL, test, protocol)
	payload, _ := json.Marshal([]string{"key1"})
	resp, err := utils.HTTPPost(subscribeURL, payload)
	err = json.Unmarshal(resp, &appResp)
	require.NoError(t, err, "error unmarshalling response")
	subscriptionID := appResp.Message
	require.NoError(t, err, "error subscribing to configuration")

	// Update a key and wait for subscriber to receive update
	targetURL := fmt.Sprintf("http://%s/update/true", externalURL)
	payload, _ = json.Marshal(items)
	var testResult *loadtest.K6RunnerMetricsSummary
	switch protocol {
	case "http":
		testResult = runk6test(t, targetURL, payload, defaultConfigSubscribeHTTPThresholdMs)
	case "grpc":
		testResult = runk6test(t, targetURL, payload, defaultConfigSubscribeGRPCThresholdMs)
	default:
		require.Fail(t, "unknown protocol")
	}

	// Unsubscribe from key `key1` in config store
	unsubscribeURL := fmt.Sprintf("http://%s/unsubscribe/%s/%s/%s", externalURL, test, protocol, string(subscriptionID))
	_, err = utils.HTTPGet(unsubscribeURL)
	require.NoError(t, err, "error unsubscribing from configuration")

	return testResult
}

func printLatency(t *testing.T, testName string, baselineResult, daprResult *loadtest.K6RunnerMetricsSummary) {
	dapr95latency := daprResult.HTTPReqDuration.Values.P95
	baseline95latency := baselineResult.HTTPReqDuration.Values.P95
	percentageIncrease := (dapr95latency - baseline95latency) / baseline95latency * 100
	t.Logf("dapr p95 latency: %.2fms, baseline p95 latency: %.2fms", dapr95latency, baseline95latency)
	t.Logf("added p95 latency: %.2fms(%.2f%%)", dapr95latency-baseline95latency, percentageIncrease)

	dapravglatency := daprResult.HTTPReqDuration.Values.Avg
	baselineavglatency := baselineResult.HTTPReqDuration.Values.Avg
	percentageIncrease = (dapravglatency - baselineavglatency) / baselineavglatency * 100
	t.Logf("dapr avg latency: %.2fms, baseline avg latency: %.2fms", dapravglatency, baselineavglatency)
	t.Logf("added avg latency: %.2fms(%.2f%%)", dapravglatency-baselineavglatency, percentageIncrease)

	appUsage, err := tr.Platform.GetAppUsage(testAppName)
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage(testAppName)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(testAppName)
	require.NoError(t, err)

	utils.LogPerfTestResourceUsage(appUsage, sidecarUsage, restarts, 0)
	summary.ForTest(t).
		Service(testName).
		CPU(appUsage.CPUm).
		Memory(appUsage.MemoryMb).
		SidecarCPU(sidecarUsage.CPUm).
		SidecarMemory(sidecarUsage.MemoryMb).
		Restarts(restarts).
		BaselineLatency(baselineavglatency).
		DaprLatency(dapravglatency).
		AddedLatency(dapravglatency - baselineavglatency).
		OutputK6([]*loadtest.K6RunnerMetricsSummary{daprResult}).
		Flush()
}

func TestConfigurationGetHTTPPerformance(t *testing.T) {
	// Get the ingress external url of test app
	externalURL := tr.Platform.AcquireAppExternalURL(testAppName)
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Check if test app endpoint is available
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Initialize the configuration updater
	url := fmt.Sprintf("http://%s/initialize-updater", externalURL)
	_, err = utils.HTTPGet(url)
	require.NoError(t, err, "error initializing configuration updater")

	// Add a key to the configuration store
	items := map[string]*Item{
		"key1": {
			Value: "val1",
		},
	}
	updateURL := fmt.Sprintf("http://%s/update/false", externalURL)
	payload, _ := json.Marshal(items)
	_, err = utils.HTTPPost(updateURL, payload)
	require.NoError(t, err, "error adding key1 to store")

	payload, _ = json.Marshal([]string{"key1"})

	t.Logf("running baseline test")
	targetURL := fmt.Sprintf("http://%s/get/baseline/http", externalURL)
	baselineResult := runk6test(t, targetURL, payload, defaultConfigGetThresholdMs)

	t.Logf("running dapr test")
	targetURL = fmt.Sprintf("http://%s/get/dapr/http", externalURL)
	daprResult := runk6test(t, targetURL, payload, defaultConfigGetThresholdMs)

	printLatency(t, "perf-configuration-get-http", baselineResult, daprResult)
}

func TestConfigurationGetGRPCPerformance(t *testing.T) {
	// Get the ingress external url of test app
	externalURL := tr.Platform.AcquireAppExternalURL(testAppName)
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Check if test app endpoint is available
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Initialize the configuration updater
	url := fmt.Sprintf("http://%s/initialize-updater", externalURL)
	_, err = utils.HTTPGet(url)
	require.NoError(t, err, "error initializing configuration updater")

	// Add a key to the configuration store
	items := map[string]*Item{
		"key1": {
			Value: "val1",
		},
	}
	updateURL := fmt.Sprintf("http://%s/update/false", externalURL)
	payload, _ := json.Marshal(items)
	_, err = utils.HTTPPost(updateURL, payload)
	require.NoError(t, err, "error adding key1 to store")

	payload, _ = json.Marshal([]string{"key1"})

	t.Logf("running baseline test")
	targetURL := fmt.Sprintf("http://%s/get/baseline/grpc", externalURL)
	baselineResult := runk6test(t, targetURL, payload, defaultConfigGetThresholdMs)

	t.Logf("running dapr test")
	targetURL = fmt.Sprintf("http://%s/get/dapr/grpc", externalURL)
	daprResult := runk6test(t, targetURL, payload, defaultConfigGetThresholdMs)

	printLatency(t, "perf-configuration-get-grpc", baselineResult, daprResult)
}

func TestConfigurationSubscribeHTTPPerformance(t *testing.T) {
	// Get the ingress external url of test app
	externalURL := tr.Platform.AcquireAppExternalURL(testAppName)
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Check if test app endpoint is available
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Initialize the configuration updater
	url := fmt.Sprintf("http://%s/initialize-updater", externalURL)
	_, err = utils.HTTPGet(url)
	require.NoError(t, err, "error initializing configuration updater")

	t.Logf("running baseline test")
	baselineResult := subscribeTest(t, externalURL, "baseline", "http")

	t.Logf("running dapr test")
	daprResult := subscribeTest(t, externalURL, "dapr", "http")

	printLatency(t, "perf-configuration-subscribe-http", baselineResult, daprResult)
}

func TestConfigurationSubscribeGRPCPerformance(t *testing.T) {
	// Get the ingress external url of test app
	externalURL := tr.Platform.AcquireAppExternalURL(testAppName)
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Check if test app endpoint is available
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Initialize the configuration updater
	url := fmt.Sprintf("http://%s/initialize-updater", externalURL)
	_, err = utils.HTTPGet(url)
	require.NoError(t, err, "error initializing configuration updater")

	t.Logf("running baseline test")
	baselineResult := subscribeTest(t, externalURL, "baseline", "grpc")

	t.Logf("running dapr test")
	daprResult := subscribeTest(t, externalURL, "dapr", "grpc")

	printLatency(t, "perf-configuration-subscribe-grpc", baselineResult, daprResult)
}
