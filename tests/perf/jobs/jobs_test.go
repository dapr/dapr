//go:build perf
// +build perf

/*
Copyright 2026 The Dapr Authors
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

package jobs_perf

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/summary"
)

const (
	numHealthChecks = 60
	appName         = "perf-jobs-service"
	testerAppName   = "tester"
	testLabel       = "jobs"

	// Target QPS for job scheduling.
	targetScheduleQPS float64 = 600

	// Target QPS for job triggering.
	targetTriggerQPS float64 = 12700
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("jobs")

	testApps := []kube.AppDescription{
		{
			AppName:           appName,
			DaprEnabled:       true,
			ImageName:         "perf-jobs",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			AppPort:           3000,
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "512Mi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
			Labels: map[string]string{
				"daprtest": testLabel + "-app",
			},
		},
		{
			AppName:           testerAppName,
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
				"daprtest": testLabel + "-app",
			},
		},
	}

	tr = runner.NewTestRunner("jobs", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

// TestJobSchedulingPerformance measures the throughput of scheduling jobs via
// the stable HTTP API using Fortio load generation.
func TestJobSchedulingPerformance(t *testing.T) {
	p := perf.Params(
		perf.WithQPS(1000),
		perf.WithConnections(16),
		perf.WithDuration("1m"),
		perf.WithPayload(`{"dueTime":"24h","data":"perftest"}`),
	)
	t.Logf("running job scheduling test with params: qps=%v, connections=%v, duration=%s",
		p.QPS, p.ClientConnections, p.TestDuration)

	testAppURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	t.Logf("waiting until test app is available: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	testerAppURL := tr.Platform.AcquireAppExternalURL(testerAppName)
	require.NotEmpty(t, testerAppURL, "tester app external URL must not be empty")

	t.Logf("waiting until tester app is available: %s", testerAppURL)
	_, err = utils.HTTPGetNTimes(testerAppURL, numHealthChecks)
	require.NoError(t, err)

	p.TargetEndpoint = "http://127.0.0.1:3500/v1.0/jobs/{uuid}"
	body, err := json.Marshal(&p)
	require.NoError(t, err)

	t.Log("running job scheduling dapr test...")
	daprResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
	t.Logf("job scheduling test results: %s", string(daprResp))
	require.NoError(t, err)
	require.NotEmpty(t, daprResp)
	require.False(t, strings.HasPrefix(string(daprResp), "error"), string(daprResp))

	appUsage, err := tr.Platform.GetAppUsage(testerAppName)
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage(testerAppName)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(testerAppName)
	require.NoError(t, err)

	utils.LogPerfTestResourceUsage(appUsage, sidecarUsage, restarts, 0)
	utils.LogPerfTestSummary(daprResp)

	var daprResult perf.TestResult
	err = json.Unmarshal(daprResp, &daprResult)
	require.NoErrorf(t, err, "failed to unmarshal: %s", string(daprResp))

	percentiles := map[int]string{2: "90th", 3: "99th"}
	for k, v := range percentiles {
		daprValue := daprResult.DurationHistogram.Percentiles[k].Value
		t.Logf("%s percentile: %.2fms", v, daprValue*1000)
	}

	t.Logf("actual QPS: %.2f, target QPS: %.0f", daprResult.ActualQPS, targetScheduleQPS)

	summary.ForTest(t).
		Service(testerAppName).
		Client(testerAppName).
		CPU(appUsage.CPUm).
		Memory(appUsage.MemoryMb).
		SidecarCPU(sidecarUsage.CPUm).
		SidecarMemory(sidecarUsage.MemoryMb).
		Restarts(restarts).
		ActualQPS(daprResult.ActualQPS).
		Params(p).
		OutputFortio(daprResult).
		Flush()

	assert.Equal(t, 0, daprResult.RetCodes.Num400)
	assert.Equal(t, 0, daprResult.RetCodes.Num500)
	assert.Equal(t, 0, restarts)
	if daprResult.ActualQPS < targetScheduleQPS {
		assert.InDelta(t, targetScheduleQPS, daprResult.ActualQPS, 100)
	}
}

func TestJobTriggerPerformance(t *testing.T) {
	testAppURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	_, err = utils.HTTPGet(fmt.Sprintf("%s/resetTriggeredCount", testAppURL))
	require.NoError(t, err)

	const (
		jobCount = 20000
		workers  = 50
	)
	jobPayload := []byte(`{"dueTime":"1s","data":"trigger-perf"}`)

	worker := func(i int) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := utils.HTTPPost(fmt.Sprintf("%s/scheduleJob/perf-%d", testAppURL, i), jobPayload)
			assert.NoError(c, err)
		}, 10*time.Second, time.Second)

		if (i+1)%5000 == 0 {
			fmt.Printf("Jobs scheduled: %d\n", i+1)
		}
	}

	var wg sync.WaitGroup
	ch := make(chan int)
	for j := 0; j < workers; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				worker(i)
			}
		}()
	}

	scheduleStart := time.Now()
	for i := 0; i < jobCount; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
	scheduleDuration := time.Since(scheduleStart)
	scheduleQPS := float64(jobCount) / scheduleDuration.Seconds()
	t.Logf("Scheduled %d jobs in %s (%.1fqps)", jobCount, scheduleDuration, scheduleQPS)

	t.Logf("Waiting for %d jobs to trigger", jobCount)
	triggerStart := time.Now()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := utils.HTTPGet(fmt.Sprintf("%s/triggeredCount", testAppURL))
		assert.NoError(c, err)
		gotCount, err := strconv.Atoi(strings.TrimSpace(string(resp)))
		assert.NoError(c, err)
		assert.GreaterOrEqual(c, gotCount, jobCount)
	}, 120*time.Second, 500*time.Millisecond)

	triggerDuration := time.Since(triggerStart)
	triggerQPS := float64(jobCount) / triggerDuration.Seconds()
	t.Logf("Triggered %d jobs in %s (%.1fqps)", jobCount, triggerDuration, triggerQPS)

	appUsage, err := tr.Platform.GetAppUsage(appName)
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage(appName)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(appName)
	require.NoError(t, err)

	utils.LogPerfTestResourceUsage(appUsage, sidecarUsage, restarts, 0)

	summary.ForTest(t).
		Service(appName).
		Client(appName).
		CPU(appUsage.CPUm).
		Memory(appUsage.MemoryMb).
		SidecarCPU(sidecarUsage.CPUm).
		SidecarMemory(sidecarUsage.MemoryMb).
		Restarts(restarts).
		ActualQPS(triggerQPS).
		Flush()

	assert.Equal(t, 0, restarts)
	if triggerQPS < targetTriggerQPS {
		assert.InDelta(t, targetTriggerQPS, triggerQPS, 500)
	}
}
