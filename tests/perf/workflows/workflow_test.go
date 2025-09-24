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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/loadtest"
	"github.com/dapr/dapr/tests/runner/summary"
	"github.com/stretchr/testify/require"
)

var (
	tr            *runner.TestRunner
	appNamePrefix = "perf-workflowsapp"
)

type K6RunConfig struct {
	TARGET_URL     string
	SCENARIO       string
	WORKFLOW_NAME  string
	WORKFLOW_INPUT string
	RATE_CHECK     string
}

func TestMain(m *testing.M) {
	backend := os.Getenv("DAPR_PERF_WORKFLOW_BACKEND_NAME")

	utils.SetupLogs("workflow_test")
	testApps := []kube.AppDescription{
		{
			AppName:           appNamePrefix + backend,
			DaprEnabled:       true,
			ImageName:         "perf-workflowsapp",
			Replicas:          1,
			IngressEnabled:    true,
			IngressPort:       3000,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "800Mi",
			DaprMemoryRequest: "800Mi",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "800Mi",
			AppPort:           -1,
		},
		{
			AppName:           appNamePrefix + backend + "-scheduler",
			DaprEnabled:       true,
			ImageName:         "perf-workflowsapp",
			Replicas:          1,
			IngressEnabled:    true,
			IngressPort:       3000,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "800Mi",
			DaprMemoryRequest: "800Mi",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "800Mi",
			AppPort:           -1,
			Config:            "featureactorreminderscheduler",
		},
	}

	tr = runner.NewTestRunner("workflow_test", testApps, []kube.ComponentDescription{}, nil)
	os.Exit(tr.Start(m))
}

func runk6test(t *testing.T, config K6RunConfig) *loadtest.K6RunnerMetricsSummary {
	k6Test := loadtest.NewK6(
		"./test.js",
		loadtest.WithParallelism(1),
		// loadtest.EnableLog(), // uncomment this to enable k6 logs, this however breaks reporting, only for debugging.
		loadtest.WithRunnerEnvVar("TARGET_URL", config.TARGET_URL),
		loadtest.WithRunnerEnvVar("SCENARIO", config.SCENARIO),
		loadtest.WithRunnerEnvVar("WORKFLOW_NAME", config.WORKFLOW_NAME),
		loadtest.WithRunnerEnvVar("WORKFLOW_INPUT", config.WORKFLOW_INPUT),
		loadtest.WithRunnerEnvVar("RATE_CHECK", config.RATE_CHECK),
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
	t.Logf("test summary `%s`", string(bts))
	return sm.RunnersResults[0]
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
		Outputf(testName+"App Memory", "%vMb", appUsage.MemoryMb).
		Outputf(testName+"App CPU", "%vm", appUsage.CPUm).
		Outputf(testName+"Sidecar Memory", "%vMb", sidecarUsage.MemoryMb).
		Outputf(testName+"Sidecar CPU", "%vm", sidecarUsage.CPUm).
		OutputInt(testName+"Restarts", restarts).
		OutputK6Trend(testName+"Req Duration", "ms", result.HTTPReqDuration).
		OutputK6Trend(testName+"Req Waiting", "ms", result.HTTPReqWaiting).
		OutputK6Trend(testName+"Iteration Duration", "ms", result.IterationDuration)
}

// Runs the test for `workflowName` workflow with different inputs and different scenarios
// inputs are the different workflow inputs/payload_sizes for which workflows are run
// scenarios are the different combinations of {VU,iterations} for which tests are run
// rateChecks[index1][index2] represents the check required for the run with input=inputs[index1] and scenario=scenarios[index2]
func testWorkflow(t *testing.T, workflowName string, testAppName string, inputs []string, scenarios []string, rateChecks [][]string, restart bool, payloadTest bool) {
	table := summary.ForTest(t)
	for index1, input := range inputs {
		for index2, scenario := range scenarios {
			subTestName := "[" + strings.ToUpper(scenario) + "]: "
			t.Run(subTestName, func(t *testing.T) {
				// Re-starting the app to clear previous run's memory
				if restart {
					log.Printf("Restarting app %s", testAppName)
					err := tr.Platform.Restart(testAppName)
					require.NoError(t, err, "Error restarting the app")
				}

				// Get the ingress external url of test app
				log.Println("acquiring app external URL")
				externalURL := tr.Platform.AcquireAppExternalURL(testAppName)
				require.NotEmpty(t, externalURL, "external URL must not be empty")

				// Check if test app endpoint is available
				require.NoError(t, utils.HealthCheckApps(externalURL))

				time.Sleep(5 * time.Second)

				// Initialize the workflow runtime
				url := fmt.Sprintf("http://%s/start-workflow-runtime", externalURL)
				// Calling start-workflow-runtime multiple times so that it is started in all app instances
				_, err := utils.HTTPGet(url)
				require.NoError(t, err, "error starting workflow runtime")

				time.Sleep(5 * time.Second)

				targetURL := fmt.Sprintf("http://%s/run-workflow", externalURL)

				config := K6RunConfig{
					TARGET_URL:     targetURL,
					SCENARIO:       scenario,
					WORKFLOW_NAME:  workflowName,
					WORKFLOW_INPUT: input,
					RATE_CHECK:     rateChecks[index1][index2],
				}

				testResult := runk6test(t, config)
				if payloadTest {
					payloadSize, _ := strconv.Atoi(input)
					table = table.Outputf(subTestName+"Payload Size", "%dKB", int(payloadSize/1000))
				}
				table = addTestResults(t, subTestName, testAppName, testResult, table)
			})
		}
	}
	err := table.Flush()
	require.NoError(t, err, "error storing test results")
}

// Runs tests for `sum_series_wf` with constant VUs
func TestWorkflowWithConstantVUs(t *testing.T) {
	workflowName := "sum_series_wf"
	inputs := []string{"100"}
	scenarios := []string{"t_30_300", "t_30_300", "t_30_300"} // t_workflowCount_iterations
	rateChecks := [][]string{{"rate==1", "rate==1", "rate==1"}}
	testWorkflow(t, workflowName, appNamePrefix, inputs, scenarios, rateChecks, false, false)
}

func TestWorkflowWithConstantIterations(t *testing.T) {
	workflowName := "sum_series_wf"
	inputs := []string{"100"}
	scenarios := []string{"t_30_300", "t_60_300", "t_90_300"} // t_workflowCount_iterations
	rateChecks := [][]string{{"rate==1", "rate==1", "rate==1"}}
	testWorkflow(t, workflowName, appNamePrefix, inputs, scenarios, rateChecks, true, false)
}

// Runs tests for `sum_series_wf` with Max VUs
func TestSeriesWorkflowWithMaxVUs(t *testing.T) {
	workflowName := "sum_series_wf"
	inputs := []string{"100"}
	scenarios := []string{"t_350_1400"} // t_workflowCount_iterations
	rateChecks := [][]string{{"rate==1"}}
	testWorkflow(t, workflowName, appNamePrefix, inputs, scenarios, rateChecks, true, false)
}

// Runs tests for `sum_parallel_wf` with Max VUs
func TestParallelWorkflowWithMaxVUs(t *testing.T) {
	workflowName := "sum_parallel_wf"
	inputs := []string{"100"}
	scenarios := []string{"t_110_440"} // t_workflowCount_iterations
	rateChecks := [][]string{{"rate==1"}}
	testWorkflow(t, workflowName, appNamePrefix, inputs, scenarios, rateChecks, true, false)
}

// Runs tests for `state_wf` with different Payload
func TestWorkflowWithDifferentPayloads(t *testing.T) {
	workflowName := "state_wf"
	scenarios := []string{"t_30_300"} // t_workflowCount_iterations
	inputs := []string{"10000", "50000", "100000"}
	rateChecks := [][]string{{"rate==1"}, {"rate==1"}, {"rate==1"}}
	testWorkflow(t, workflowName, appNamePrefix, inputs, scenarios, rateChecks, true, true)
}
