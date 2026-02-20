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

package pubsub_publish

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	// Number of times to check for endpoint health per app.
	numHealthChecks = 60
	appName         = "pubsub-perf-grpc"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("pubsub_publish_grpc")

	testApps := []kube.AppDescription{
		{
			AppName:           appName,
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

	tr = runner.NewTestRunner("pubsub_publish_grpc", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestPubsubPublishGrpcPerformance(t *testing.T) {
	p := perf.Params(
		perf.WithQPS(1000),
		perf.WithConnections(16),
		perf.WithDuration("1m"),
		perf.WithPayloadSize(0),
	)
	t.Logf("running pubsub publish grpc test with params: qps=%v, connections=%v, duration=%s, payload size=%v, payload=%v", p.QPS, p.ClientConnections, p.TestDuration, p.PayloadSizeKB, p.Payload)

	// Get the ingress external url of tester app
	testerAppURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, testerAppURL, "tester app external URL must not be empty")

	// Check if tester app endpoint is available
	t.Logf("tester app url: %s", testerAppURL)
	_, err := utils.HTTPGetNTimes(testerAppURL, numHealthChecks)
	require.NoError(t, err)

	// Perform baseline test
	p.Grpc = true
	p.Dapr = "capability=pubsub,target=noop"
	p.TargetEndpoint = fmt.Sprintf("http://localhost:50001")
	body, err := json.Marshal(&p)
	require.NoError(t, err)

	t.Log("running baseline test...")
	var baselineResp []byte
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		baselineResp, err = utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
		assert.NoError(c, err, "baseline test HTTP POST failed")
	}, 90*time.Second, 2*time.Second, "baseline test failed after retries")

	t.Logf("baseline test results: %s", string(baselineResp))
	require.NotEmpty(t, baselineResp)
	require.False(t, strings.HasPrefix(string(baselineResp), "error"))

	// Perform dapr test
	p.Dapr = "capability=pubsub,target=dapr,method=publish,store=inmemorypubsub,topic=topic123,contenttype=text/plain"
	p.TargetEndpoint = fmt.Sprintf("http://localhost:50001")
	body, err = json.Marshal(&p)
	require.NoError(t, err)

	t.Log("running dapr test...")
	var daprResp []byte
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		daprResp, err = utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
		assert.NoError(c, err, "dapr test HTTP POST failed")
	}, 90*time.Second, 2*time.Second, "dapr test failed after retries")

	t.Logf("dapr test results: %s", string(daprResp))
	require.NotEmpty(t, daprResp)
	require.False(t, strings.HasPrefix(string(daprResp), "error"))

	sidecarUsage, err := tr.Platform.GetSidecarUsage(appName)
	require.NoError(t, err)

	appUsage, err := tr.Platform.GetAppUsage(appName)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(appName)
	require.NoError(t, err)

	utils.LogPerfTestResourceUsage(appUsage, sidecarUsage, restarts, 0)
	var daprResult perf.TestResult
	err = json.Unmarshal(daprResp, &daprResult)
	require.NoError(t, err)
	utils.LogPerfTestSummary(daprResp)

	var baselineResult perf.TestResult
	err = json.Unmarshal(baselineResp, &baselineResult)
	require.NoError(t, err)

	percentiles := []string{"50th", "75th", "90th", "99th"}
	var tp90Latency float64

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
	baselineLatency := baselineResult.DurationHistogram.Avg * 1000
	daprLatency := daprResult.DurationHistogram.Avg * 1000
	t.Logf("baseline latency avg: %sms", fmt.Sprintf("%.2f", baselineLatency))
	t.Logf("dapr latency avg: %sms", fmt.Sprintf("%.2f", daprLatency))
	t.Logf("added latency avg: %sms", fmt.Sprintf("%.2f", avg))

	summary.ForTest(t).
		Service(appName).
		CPU(appUsage.CPUm).
		Memory(appUsage.MemoryMb).
		SidecarCPU(sidecarUsage.CPUm).
		SidecarMemory(sidecarUsage.MemoryMb).
		Restarts(restarts).
		BaselineLatency(baselineLatency).
		DaprLatency(daprLatency).
		AddedLatency(avg).
		ActualQPS(daprResult.ActualQPS).
		Params(p).
		OutputFortio(daprResult).
		Flush()

	require.Equal(t, 0, daprResult.RetCodes.Num400)
	require.Equal(t, 0, daprResult.RetCodes.Num500)
	require.Equal(t, 0, restarts)
	require.True(t, daprResult.ActualQPS > float64(p.QPS)*0.99)
	require.Greater(t, tp90Latency, 0.0)
	require.LessOrEqual(t, tp90Latency, 2.0)
}
