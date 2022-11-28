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

package actor_double_activation

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
	numHealthChecks        = 60 // Number of times to check for endpoint health per app.
	serviceApplicationName = "perf-actor-double-activation"
)

var (
	tr *runner.TestRunner
)

func TestMain(m *testing.M) {
	utils.SetupLogs("actor_double_activation")

	testApps := []kube.AppDescription{
		{
			AppName:           serviceApplicationName,
			DaprEnabled:       true,
			ImageName:         "perf-actor-activation-locker",
			Config:            "redishostconfig",
			Replicas:          3,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			AppPort:           3000,
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "512Mi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "512Mi",
			AppMemoryRequest:  "256Mi",
		},
	}

	tr = runner.NewTestRunner("actordoubleactivation", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

type ActivationMetrics struct {
	loadtest.K6RunnerMetricsSummary `json:",inline"`
	DoubleActivation                loadtest.K6CounterMetric `json:"double_activations"`
}

func TestActorDoubleActivation(t *testing.T) {
	// Get the ingress external url of test app
	testServiceAppURL := tr.Platform.AcquireAppExternalURL(serviceApplicationName)
	require.NotEmpty(t, testServiceAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testServiceAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testServiceAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	k6Test := loadtest.NewK6("./test.js", loadtest.WithParallelism(3))
	defer k6Test.Dispose()
	t.Log("running the k6 load test...")
	require.NoError(t, tr.Platform.LoadTest(k6Test))
	summary, err := loadtest.K6Result[ActivationMetrics](k6Test)
	require.NoError(t, err)
	require.NotNil(t, summary)
	bts, err := json.MarshalIndent(summary, "", " ")
	require.NoError(t, err)
	require.True(t, summary.Pass, fmt.Sprintf("test has not passed, results %s", string(bts)))
	t.Logf("test summary `%s`", string(bts))
}
