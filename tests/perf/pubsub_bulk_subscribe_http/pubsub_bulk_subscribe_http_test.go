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
	"testing"

	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/loadtest"
	"github.com/stretchr/testify/require"
)

const (
	numHealthChecks = 60 // Number of times to check for endpoint health per app.
	numberOfActors  = 100
	testAppName     = "testapp"
	testLabel       = "pubsub_bulk_subscribe_http"
)

var (
	tr          *runner.TestRunner
	actorsTypes string
)

func TestMain(m *testing.M) {
	utils.SetupLogs(testLabel)

	testApps := []kube.AppDescription{
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
		},
	}

	tr = runner.NewTestRunner(testLabel, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestPubsubBulkSubscribeHttpPerformance(t *testing.T) {
	// Get the ingress external url of test app
	testAppURL := tr.Platform.AcquireAppExternalURL(testAppName)
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	k6Test := loadtest.NewK6("./test.js",
		loadtest.WithParallelism(1),
		loadtest.WithRunnerEnvVar("TARGET_URL", testAppURL),
		loadtest.WithRunnerEnvVar("PUBSUB_NAME", "inmemorypubsub"))
	defer k6Test.Dispose()

	t.Log("running the k6 load test...")
	require.NoError(t, tr.Platform.LoadTest(k6Test))
	summary, err := loadtest.K6ResultDefault(k6Test)
	require.NoError(t, err)
	require.NotNil(t, summary)
	bts, err := json.MarshalIndent(summary, "", " ")
	require.NoError(t, err)
	require.True(t, summary.Pass, fmt.Sprintf("test has not passed, results %s", string(bts)))
	t.Logf("test summary `%s`", string(bts))

	appUsage, err := tr.Platform.GetAppUsage(testAppName)
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage(testAppName)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(testAppName)
	require.NoError(t, err)

	t.Logf("target dapr app consumed %vm CPU and %vMb of Memory", appUsage.CPUm, appUsage.MemoryMb)
	t.Logf("target dapr sidecar consumed %vm CPU and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)
	t.Logf("target dapr app or sidecar restarted %v times", restarts)
	require.Equal(t, 0, restarts)
}
