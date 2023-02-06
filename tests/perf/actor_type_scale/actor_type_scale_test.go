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

package actor_type_scale

import (
	"encoding/json"
	"fmt"
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
	serviceApplicationName = "perf-actor-type"
	numberOfActors         = 100
)

var (
	tr          *runner.TestRunner
	actorsTypes string
)

func TestMain(m *testing.M) {
	actorsTypes = "actor_0"

	for i := 1; i < numberOfActors; i++ {
		actorsTypes += fmt.Sprintf(",actor_%d", i)
	}
	utils.SetupLogs("actor_id_stress_test")

	testApps := []kube.AppDescription{
		{
			AppName:           serviceApplicationName,
			DaprEnabled:       true,
			ImageName:         "perf-actorfeatures",
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
			AppMemoryLimit:    "512Mi",
			AppMemoryRequest:  "256Mi",
			AppEnv: map[string]string{
				"TEST_APP_ACTOR_TYPE": actorsTypes,
			},
		},
	}

	tr = runner.NewTestRunner("actoridstress", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorIdStress(t *testing.T) {
	// Get the ingress external url of test app
	testServiceAppURL := tr.Platform.AcquireAppExternalURL(serviceApplicationName)
	require.NotEmpty(t, testServiceAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testServiceAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testServiceAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	k6Test := loadtest.NewK6("./test.js", loadtest.WithParallelism(1), loadtest.WithRunnerEnvVar("ACTORS_TYPES", actorsTypes))
	// defer k6Test.Dispose()
	t.Log("running the k6 load test...")
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

	t.Logf("target dapr app consumed %vm CPU and %vMb of Memory", appUsage.CPUm, appUsage.MemoryMb)
	t.Logf("target dapr sidecar consumed %vm CPU and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)
	t.Logf("target dapr app or sidecar restarted %v times", restarts)

	require.True(t, sm.Pass, fmt.Sprintf("test has not passed, results %s", string(bts)))
	require.Equal(t, 0, restarts)
	t.Logf("test summary `%s`", string(bts))
}
