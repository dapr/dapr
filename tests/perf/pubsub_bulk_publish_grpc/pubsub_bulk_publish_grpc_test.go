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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const numHealthChecks = 60 // Number of times to check for endpoint health per app.

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("pubsub_bulk_publish_grpc")

	testApps := []kube.AppDescription{
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
		},
	}

	tr = runner.NewTestRunner("pubsub_bulk_publish_grpc", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestBulkPubsubPublishGrpcPerformance(t *testing.T) {
	tcs := []struct {
		name         string
		isRawPayload bool
	}{
		{
			name:         "with cloud event",
			isRawPayload: false,
		},
		{
			name:         "without cloud event (raw payload)",
			isRawPayload: true,
		},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			p := perf.Params()
			t.Logf("running pubsub bulk publish grpc test with params: qps=%v, connections=%v, duration=%s, payload size=%v, payload=%v", p.QPS, p.ClientConnections, p.TestDuration, p.PayloadSizeKB, p.Payload)

			// Get the ingress external url of tester app
			testerAppURL := tr.Platform.AcquireAppExternalURL("tester")
			require.NotEmpty(t, testerAppURL, "tester app external URL must not be empty")

			// Check if tester app endpoint is available
			t.Logf("tester app url: %s", testerAppURL)
			_, err := utils.HTTPGetNTimes(testerAppURL, numHealthChecks)
			require.NoError(t, err)

			// Perform baseline test - publish 1000 messages with individual Publish calls
			p.Grpc = true
			p.Dapr = "capability=pubsub,target=dapr,method=publish-multi,store=inmemorypubsub,topic=topic123,contenttype=text/plain,numevents=1000"
			if tc.isRawPayload {
				p.Dapr += ",rawpayload=true"
			}
			p.TargetEndpoint = fmt.Sprintf("http://localhost:50001")
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

			// Perform dapr test - publish 1000 messages with a single BulkPublish call
			p.Dapr = "capability=pubsub,target=dapr,method=bulkpublish,store=inmemorypubsub,topic=topic123,contenttype=text/plain,numevents=1000"
			if tc.isRawPayload {
				p.Dapr += ",rawpayload=true"
			}
			p.TargetEndpoint = fmt.Sprintf("http://localhost:50001")
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

			sidecarUsage, err := tr.Platform.GetSidecarUsage("tester")
			require.NoError(t, err)

			appUsage, err := tr.Platform.GetAppUsage("tester")
			require.NoError(t, err)

			restarts, err := tr.Platform.GetTotalRestarts("tester")
			require.NoError(t, err)

			t.Logf("dapr sidecar consumed %vm Cpu and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)

			var daprResult perf.TestResult
			err = json.Unmarshal(daprResp, &daprResult)
			require.NoError(t, err)

			var baselineResult perf.TestResult
			err = json.Unmarshal(baselineResp, &baselineResult)
			require.NoError(t, err)

			percentiles := []string{"50th", "75th", "90th", "99th"}
			var tp90Latency float64

			for k, v := range percentiles {
				bulkValue := daprResult.DurationHistogram.Percentiles[k].Value
				baselineValue := baselineResult.DurationHistogram.Percentiles[k].Value

				latency := (baselineValue - bulkValue) * 1000
				if v == "90th" {
					tp90Latency = latency
				}
				t.Logf("reduced latency for %s percentile: %sms", v, fmt.Sprintf("%.2f", latency))
			}
			avg := (baselineResult.DurationHistogram.Avg - daprResult.DurationHistogram.Avg) * 1000
			t.Logf("baseline latency avg: %sms", fmt.Sprintf("%.2f", baselineResult.DurationHistogram.Avg*1000))
			t.Logf("dapr latency avg: %sms", fmt.Sprintf("%.2f", daprResult.DurationHistogram.Avg*1000))
			t.Logf("reduced latency avg: %sms", fmt.Sprintf("%.2f", avg))

			report := perf.NewTestReport(
				[]perf.TestResult{baselineResult, daprResult},
				"Pubsub Bulk Publish Grpc",
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
		})
	}
}
