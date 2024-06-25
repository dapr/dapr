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

package actor_double_activation

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/loadtest"
	"github.com/dapr/dapr/tests/runner/summary"
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

func TestActorDoubleActivation(t *testing.T) {
	// Get the ingress external url of test app
	testServiceAppURL := tr.Platform.AcquireAppExternalURL(serviceApplicationName)
	require.NotEmpty(t, testServiceAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("Test app url: %s", testServiceAppURL)
	err := utils.HealthCheckApps(testServiceAppURL + "/health")
	require.NoError(t, err, "Health checks failed")

	k6Test := loadtest.NewK6(
		"./test.js",
		loadtest.WithParallelism(3),
		loadtest.WithRunnerEnvVar("TEST_APP_NAME", serviceApplicationName),
	)
	defer k6Test.Dispose()

	t.Log("Running the k6 load test...")
	require.NoError(t, tr.Platform.LoadTest(k6Test))
	sm, err := loadtest.K6ResultDefault(k6Test)
	require.NoError(t, err)
	require.NotNil(t, sm)
	bts, err := json.MarshalIndent(sm, "", " ")
	require.NoError(t, err)

	appUsage, err := tr.Platform.GetAppUsage(serviceApplicationName)
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage(serviceApplicationName)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(serviceApplicationName)
	require.NoError(t, err)

	summary.ForTest(t).
		Service(serviceApplicationName).
		CPU(appUsage.CPUm).
		Memory(appUsage.MemoryMb).
		SidecarCPU(sidecarUsage.CPUm).
		SidecarMemory(sidecarUsage.MemoryMb).
		Restarts(restarts).
		OutputK6(sm.RunnersResults).
		Flush()

	require.Truef(t, sm.Pass, "test has not passed, results: %s", string(bts))
	t.Logf("Test summary `%s`", string(bts))
}
