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

package pubsub_bulk_publish

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/summary"
)

const (
	// Number of times to check for endpoint health per app.
	numHealthChecks      = 60
	numMessagesToPublish = 100
	appName              = "pubsub-perf-bulk-grpc"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("pubsub_bulk_publish_grpc")

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

	tr = runner.NewTestRunner("pubsub_bulk_publish_grpc", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestBulkPubsubPublishGrpcPerformance(t *testing.T) {
	tcs := []struct {
		name         string
		pubsub       string
		isRawPayload bool
	}{
		{
			name:         "In-memory with with cloud event",
			pubsub:       "inmemorypubsub",
			isRawPayload: false,
		},
		{
			name:         "In-memory without cloud event (raw payload)",
			pubsub:       "inmemorypubsub",
			isRawPayload: true,
		},
		{
			name:         "Kafka with cloud event",
			pubsub:       "kafka-messagebus",
			isRawPayload: false,
		},
		{
			name:         "Kafka without cloud event (raw payload)",
			pubsub:       "kafka-messagebus",
			isRawPayload: true,
		},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			p := perf.Params(
				perf.WithQPS(200),
				perf.WithConnections(16),
				perf.WithDuration("1m"),
				perf.WithPayloadSize(1024),
			)
			t.Logf("running pubsub bulk publish grpc test with params: qps=%v, connections=%v, duration=%s, payload size=%v, payload=%v", p.QPS, p.ClientConnections, p.TestDuration, p.PayloadSizeKB, p.Payload)

			// Get the ingress external url of tester app
			testerAppURL := tr.Platform.AcquireAppExternalURL(appName)
			require.NotEmpty(t, testerAppURL, "tester app external URL must not be empty")

			// Check if tester app endpoint is available
			t.Logf("tester app url: %s", testerAppURL)
			_, err := utils.HTTPGetNTimes(testerAppURL, numHealthChecks)
			require.NoError(t, err)

			// Perform baseline test - publish messages with individual Publish calls
			p.Grpc = true
			p.Dapr = fmt.Sprintf("capability=pubsub,target=dapr,method=publish,store=%s,topic=topic123,contenttype=text/plain,numevents=%d,rawpayload=%s", tc.pubsub, numMessagesToPublish, strconv.FormatBool(tc.isRawPayload))
			p.TargetEndpoint = fmt.Sprintf("http://localhost:50001")
			body, err := json.Marshal(&p)
			require.NoError(t, err)

			t.Log("running baseline test, publishing messages individually...")
			baselineResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
			t.Logf("baseline test results: %s", string(baselineResp))
			t.Log("checking err...")
			require.NoError(t, err)
			require.NotEmpty(t, baselineResp)
			// fast fail if daprResp starts with error
			require.False(t, strings.HasPrefix(string(baselineResp), "error"))

			// Perform bulk test - publish messages with a single BulkPublish call
			p.Dapr = fmt.Sprintf("capability=pubsub,target=dapr,method=bulkpublish,store=%s,topic=topic123,contenttype=text/plain,numevents=%d,rawpayload=%s", tc.pubsub, numMessagesToPublish, strconv.FormatBool(tc.isRawPayload))
			p.TargetEndpoint = fmt.Sprintf("http://localhost:50001")
			body, err = json.Marshal(&p)
			require.NoError(t, err)

			t.Log("running bulk test, publishing messages with bulk publish...")
			bulkResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
			t.Logf("bulk test results: %s", string(bulkResp))
			t.Log("checking err...")
			require.NoError(t, err)
			require.NotEmpty(t, bulkResp)
			// fast fail if bulkResp starts with error
			require.False(t, strings.HasPrefix(string(bulkResp), "error"))

			sidecarUsage, err := tr.Platform.GetSidecarUsage(appName)
			require.NoError(t, err)

			appUsage, err := tr.Platform.GetAppUsage(appName)
			require.NoError(t, err)

			restarts, err := tr.Platform.GetTotalRestarts(appName)
			require.NoError(t, err)

			t.Logf("dapr sidecar consumed %vm Cpu and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)

			var bulkResult perf.TestResult
			err = json.Unmarshal(bulkResp, &bulkResult)
			require.NoError(t, err)

			var baselineResult perf.TestResult
			err = json.Unmarshal(baselineResp, &baselineResult)
			require.NoError(t, err)

			percentiles := []string{"50th", "75th", "90th", "99th"}
			var tp90Latency float64

			for k, v := range percentiles {
				bulkValue := bulkResult.DurationHistogram.Percentiles[k].Value
				baselineValue := baselineResult.DurationHistogram.Percentiles[k].Value

				latency := (baselineValue - bulkValue) * 1000
				if v == "90th" {
					tp90Latency = latency
				}
				t.Logf("reduced latency for %s percentile: %sms", v, fmt.Sprintf("%.2f", latency))
			}
			avg := (baselineResult.DurationHistogram.Avg - bulkResult.DurationHistogram.Avg) * 1000
			baselineLatency := baselineResult.DurationHistogram.Avg * 1000
			bulkLatencyMS := fmt.Sprintf("%.2f", bulkResult.DurationHistogram.Avg*1000)
			reducedLatencyMS := fmt.Sprintf("%.2f", avg)
			t.Logf("baseline latency avg: %sms", fmt.Sprintf("%.2f", baselineLatency))
			t.Logf("bulk latency avg: %sms", bulkLatencyMS)
			t.Logf("reduced latency avg: %sms", reducedLatencyMS)

			t.Logf("baseline QPS: %v", baselineResult.ActualQPS)
			t.Logf("bulk QPS: %v", bulkResult.ActualQPS)
			increaseQPS := (bulkResult.ActualQPS - baselineResult.ActualQPS) / baselineResult.ActualQPS * 100
			t.Logf("increase in QPS: %v", increaseQPS)

			summary.ForTest(t).
				Service(appName).
				CPU(appUsage.CPUm).
				Memory(appUsage.MemoryMb).
				SidecarCPU(sidecarUsage.CPUm).
				SidecarMemory(sidecarUsage.MemoryMb).
				Restarts(restarts).
				BaselineLatency(baselineLatency).
				Outputf("Bulk latency avg", "%sms", bulkLatencyMS).
				Outputf("Reduced latency avg", "%sms", reducedLatencyMS).
				OutputFloat64("Baseline QPS", baselineResult.ActualQPS).
				OutputFloat64("Increase in QPS", increaseQPS).
				ActualQPS(bulkResult.ActualQPS).
				Params(p).
				OutputFortio(bulkResult).
				Flush()

			report := perf.NewTestReport(
				[]perf.TestResult{baselineResult, bulkResult},
				"Pubsub Bulk Publish Grpc",
				sidecarUsage,
				appUsage)
			report.SetTotalRestartCount(restarts)
			err = utils.UploadAzureBlob(report)

			if err != nil {
				t.Error(err)
			}

			require.Equal(t, 0, bulkResult.RetCodes.Num400)
			require.Equal(t, 0, bulkResult.RetCodes.Num500)
			require.Equal(t, 0, restarts)
			require.True(t, bulkResult.ActualQPS > float64(p.QPS)*0.99)
			require.Greater(t, tp90Latency, 0.0)
		})
	}
}
