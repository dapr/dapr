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

package workflows

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/loadtest"
	"github.com/dapr/dapr/tests/runner/summary"
	"github.com/stretchr/testify/require"
)

var tr *runner.TestRunner

var testAppNames = []string{"perf-workflowsapp"}

type K6RunConfig struct {
	TARGET_URL     string
	SCENARIO       string
	WORKFLOW_NAME  string
	WORKFLOW_INPUT string
	RATE_CHECK     string
}

func TestMain(m *testing.M) {
	utils.SetupLogs("workflow_test")
	testApps := []kube.AppDescription{}
	for _, testAppName := range testAppNames {
		const replicas = 1
		testApps = append(testApps, kube.AppDescription{
			AppName:           testAppName,
			DaprEnabled:       true,
			ImageName:         "perf-workflowsapp",
			Replicas:          replicas,
			IngressEnabled:    true,
			IngressPort:       3000,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "800Mi",
			DaprMemoryRequest: "800Mi",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "800Mi",
			AppPort:           -1,
		})
	}

	tr = runner.NewTestRunner("workflow_test", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func collect(t *testing.T, testAppName string, table *summary.Table) {
	appUsage, err1 := tr.Platform.GetAppUsage(testAppName)
	sidecarUsage, err2 := tr.Platform.GetSidecarUsage(testAppName)
	if err1 == nil {
		table.
		Outputf(testAppName+"App Memory", "%vMb--at time %v", appUsage.MemoryMb, time.Now()).
		Outputf(testAppName+"App CPU", "%vm--at time %v", appUsage.CPUm, time.Now())
	}
	if err2 == nil {
		table.
		Outputf(testAppName+"Sidecar Memory", "%vMb--at time %v", sidecarUsage.MemoryMb, time.Now()).
		Outputf(testAppName+"Sidecar CPU", "%vm--at time %v", sidecarUsage.CPUm, time.Now())
	}
}

func collectCPUMemoryUsage(t *testing.T, testAppName string, table *summary.Table, num int) {
	done := make(chan bool)
	time.AfterFunc(time.Duration(num) * time.Second, func() {
		done <- true
	})

	ticker := time.NewTicker(1 * time.Second)

	for {
		select {
		case <-ticker.C:
			go collect(t, testAppName, table) // Start a new goroutine collect metrics
		case <-done:
			ticker.Stop()
			return
		}
	}
}

func addTestResults(t *testing.T, testName string, testAppName string, result *loadtest.K6RunnerMetricsSummary, table *summary.Table) *summary.Table {
	appUsage, err := tr.Platform.GetAppUsage(testAppName)
	require.NoError(t, err)
	sidecarUsage, err := tr.Platform.GetSidecarUsage(testAppName)
	require.NoError(t, err)
	restarts, err := tr.Platform.GetTotalRestarts(testAppName)
	require.NoError(t, err)

	return table.
		OutputInt(testName+"VUs Max", result.VusMax.Values.Max).
		OutputFloat64(testName+"Iterations Count", result.Iterations.Values.Count).
		Outputf(testAppName+"App Memory", "%vMb--at time %v", appUsage.MemoryMb, time.Now()).
		Outputf(testAppName+"App CPU", "%vm--at time %v", appUsage.CPUm, time.Now()).
		Outputf(testAppName+"Sidecar Memory", "%vMb--at time %v", sidecarUsage.MemoryMb, time.Now()).
		Outputf(testAppName+"Sidecar CPU", "%vm--at time %v", sidecarUsage.CPUm, time.Now()).
		OutputInt(testName+"Restarts", restarts).
		OutputK6Trend(testName+"Req Duration", "ms", result.HTTPReqDuration).
		OutputK6Trend(testName+"Req Waiting", "ms", result.HTTPReqWaiting).
		OutputK6Trend(testName+"Iteration Duration", "ms", result.IterationDuration)
}

func TestWorkFlowPerf(t *testing.T) {
	table := summary.ForTest(t)
	tcs := []struct {
		name         		string
		rateCheck       	string
		enableMemoryCheck	bool
	}{
		{
			name:         "average_load",
			rateCheck:    "rate==1",
			enableMemoryCheck: true,

		},
		// {
		// 	name:         "stress_load",
		// 	rateCheck:    "rate==1",
		// 	enableMemoryCheck: true,

		// },
		// {
		// 	name:         "stress_load",
		// 	rateCheck:    "rate==1",
		// 	enableMemoryCheck: false,
		// },
		// {
		// 	name:         "ramp_up",
		// 	rateCheck:    "rate>0.9",
		// 	enableMemoryCheck: true,
		// },
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			subTestName := "[" + tc.name +"]: "
			// Get the ingress external url of test app
			log.Println("acquiring app external URL")
			externalURL := tr.Platform.AcquireAppExternalURL(testAppNames[0])
			require.NotEmpty(t, externalURL, "external URL must not be empty")

			// Check if test app endpoint is available
			require.NoError(t, utils.HealthCheckApps(externalURL))

			time.Sleep(2 * time.Second)

			// Initialize the workflow runtime
			url := fmt.Sprintf("http://%s/start-workflow-runtime", externalURL)
			// Calling start-workflow-runtime multiple times so that it is started in all app instances
			_, err := utils.HTTPGet(url)
			require.NoError(t, err, "error starting workflow runtime")
			
			time.Sleep(2 * time.Second)

			targetURL := fmt.Sprintf("http://%s/run-workflow", externalURL)

			k6Test := loadtest.NewK6(
				"./test.js",
				loadtest.WithParallelism(1),
				// loadtest.EnableLog(), // uncomment this to enable k6 logs, this however breaks reporting, only for debugging.
				loadtest.WithRunnerEnvVar("TARGET_URL", targetURL),
				loadtest.WithRunnerEnvVar("SCENARIO", "smoke_test"),
				loadtest.WithRunnerEnvVar("RATE_CHECK", "rate==1"),
			)
			defer k6Test.Dispose()
			t.Log("running the k6 load test...")
			
			if tc.enableMemoryCheck {
				go collectCPUMemoryUsage(t, testAppNames[0], table, 15)
			}
		
			require.NoError(t, tr.Platform.LoadTest(k6Test))
			sm, err := loadtest.K6ResultDefault(k6Test)
			require.NoError(t, err)
			require.NotNil(t, sm)
			bts, err := json.MarshalIndent(sm, "", " ")
			require.NoError(t, err)
			require.True(t, sm.Pass, fmt.Sprintf("test has not passed, results %s", string(bts)))
			t.Logf("test summary `%s`", string(bts))
			testResult :=sm.RunnersResults[0]
		
			table = addTestResults(t, subTestName, testAppNames[0], testResult, table)
		
			time.Sleep(2 * time.Second)
		
			// Stop the workflow runtime
			url = fmt.Sprintf("http://%s/shutdown-workflow-runtime", externalURL)
			_, err = utils.HTTPGet(url)
			require.NoError(t, err, "error shutdown workflow runtime")
		
			err = table.Flush()
			require.NoError(t, err, "error storing test results")
		})
	}
}