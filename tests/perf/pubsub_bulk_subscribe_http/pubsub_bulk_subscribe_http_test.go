//go:build perf
// +build perf

/*
Copyright 2022 The Dapr Authors
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

package pubsub_bulk_subscribe_http

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

const (
	numHealthChecks = 60 // Number of times to check for endpoint health per app.
	testAppName     = "testapp"
	bulkTestAppName = "testapp-bulk"
	testLabel       = "pubsub_bulk_subscribe_http"

	defaultBulkPublishSubscribeHttpThresholdMs     = 1500
	defaultBulkPublishBulkSubscribeHttpThresholdMs = 500
)

var (
	tr          *runner.TestRunner
	actorsTypes string
)

func TestMain(m *testing.M) {
	utils.SetupLogs(testLabel)

	testApps := []kube.AppDescription{
		{
			AppName:           bulkTestAppName,
			DaprEnabled:       true,
			ImageName:         "perf-pubsub_subscribe_http",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			AppPort:           3000,
			AppProtocol:       "http",
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "512Mi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
			Labels: map[string]string{
				"daprtest": testLabel + "-" + bulkTestAppName,
			},
			AppEnv: map[string]string{
				"SUBSCRIBE_TYPE": "bulk",
			},
		},
		{
			AppName:           testAppName,
			DaprEnabled:       true,
			ImageName:         "perf-pubsub_subscribe_http",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			AppPort:           3000,
			AppProtocol:       "http",
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "512Mi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
			Labels: map[string]string{
				"daprtest": testLabel + "-" + testAppName,
			},
			AppEnv: map[string]string{
				"SUBSCRIBE_TYPE": "normal",
			},
		},
	}

	tr = runner.NewTestRunner(testLabel, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func runTest(t *testing.T, testAppURL, publishType, subscribeType, httpReqDurationThresholdMs string) {
	t.Logf("Starting test with subscribe type %s", subscribeType)

	k6Test := loadtest.NewK6("./test.js",
		loadtest.WithParallelism(1),
		// loadtest.EnableLog(), // uncomment this to enable k6 logs, this however breaks reporting, only for debugging.
		loadtest.WithRunnerEnvVar("TARGET_URL", testAppURL),
		loadtest.WithRunnerEnvVar("PUBSUB_NAME", "kafka-messagebus"),
		loadtest.WithRunnerEnvVar("PUBLISH_TYPE", publishType),
		loadtest.WithRunnerEnvVar("SUBSCRIBE_TYPE", subscribeType),
		loadtest.WithRunnerEnvVar("HTTP_REQ_DURATION_THRESHOLD", httpReqDurationThresholdMs))
	defer k6Test.Dispose()

	t.Log("running the k6 load test...")
	require.NoError(t, tr.Platform.LoadTest(k6Test))
	sm, err := loadtest.K6ResultDefault(k6Test)
	require.NoError(t, err)
	require.NotNil(t, sm)

	appUsage, err := tr.Platform.GetAppUsage(testAppName)
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage(testAppName)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(testAppName)
	require.NoError(t, err)

	summary.ForTest(t).
		Service(testAppName).
		CPU(appUsage.CPUm).
		Memory(appUsage.MemoryMb).
		SidecarCPU(sidecarUsage.CPUm).
		SidecarMemory(sidecarUsage.MemoryMb).
		Restarts(restarts).
		OutputK6(sm.RunnersResults).
		Output("TARGET_URL", testAppURL).
		Output("PUBLISH_TYPE", publishType).
		Output("SUBSCRIBE_TYPE", subscribeType).
		Output("HTTP_REQ_DURATION_THRESHOLD", httpReqDurationThresholdMs).
		Flush()

	t.Logf("target dapr app consumed %vm CPU and %vMb of Memory", appUsage.CPUm, appUsage.MemoryMb)
	t.Logf("target dapr sidecar consumed %vm CPU and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)
	t.Logf("target dapr app or sidecar restarted %v times", restarts)
	bts, err := json.MarshalIndent(sm, "", " ")
	require.NoError(t, err)
	require.True(t, sm.Pass, fmt.Sprintf("test has not passed, results %s", string(bts)))
	t.Logf("test summary `%s`", string(bts))
	require.Equal(t, 0, restarts)
}

func TestPubsubBulkPublishSubscribeHttpPerformance(t *testing.T) {
	// Get the ingress external url of test app
	testAppURL := tr.Platform.AcquireAppExternalURL(testAppName)
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	threshold := os.Getenv("DAPR_PERF_PUBSUB_SUBSCRIBE_HTTP_THRESHOLD")
	if threshold == "" {
		threshold = strconv.Itoa(defaultBulkPublishSubscribeHttpThresholdMs)
	}
	runTest(t, testAppURL, "bulk", "normal", threshold)
}

func TestPubsubBulkPublishBulkSubscribeHttpPerformance(t *testing.T) {
	// Get the ingress external url of test app
	bulkTestAppURL := tr.Platform.AcquireAppExternalURL(bulkTestAppName)
	require.NotEmpty(t, bulkTestAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("bulk test app url: %s", bulkTestAppURL+"/health")
	_, err := utils.HTTPGetNTimes(bulkTestAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	threshold := os.Getenv("DAPR_PERF_PUBSUB_BULK_SUBSCRIBE_HTTP_THRESHOLD")
	if threshold == "" {
		threshold = strconv.Itoa(defaultBulkPublishBulkSubscribeHttpThresholdMs)
	}
	runTest(t, bulkTestAppURL, "bulk", "bulk", threshold)
}
