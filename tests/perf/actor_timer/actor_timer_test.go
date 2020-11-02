// +build perf

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actor_timer_with_state_perf

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const (
	numHealthChecks = 60  // Number of times to check for endpoint health per app.
	maxAppMemMb     = 300 // Maximum Mb for app memory allocation.
	maxSidecarMemMb = 100 // Maximum Mb for sidecar memory allocation.
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	testApps := []kube.AppDescription{
		{
			AppName:        "testapp",
			DaprEnabled:    true,
			ImageName:      "perf-actorjava",
			Replicas:       4,
			IngressEnabled: true,
			AppPort:        3000,
		},
		{
			AppName:        "tester",
			DaprEnabled:    true,
			ImageName:      "perf-tester",
			Replicas:       1,
			IngressEnabled: true,
			AppPort:        3001,
		},
	}

	tr = runner.NewTestRunner("actortimerwithstate", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorTimerWithStatePerformance(t *testing.T) {
	p := perf.ParamsFromDefaults()

	if val, ok := os.LookupEnv(perf.ClientConnectionsEnvVar); ok && val != "" {
		clientConn, err := strconv.Atoi(val)
		require.NoError(t, err)
		p.ClientConnections = clientConn
	}
	if val, ok := os.LookupEnv(perf.TestDurationEnvVar); ok && val != "" {
		p.TestDuration = val
	}
	if val, ok := os.LookupEnv(perf.PayloadSizeEnvVar); ok && val != "" {
		payloadSize, err := strconv.Atoi(val)
		require.NoError(t, err)
		p.PayloadSizeKB = payloadSize
	}
	if val, ok := os.LookupEnv(perf.PayloadEnvVar); ok && val != "" {
		p.Payload = val
	}
	if val, ok := os.LookupEnv(perf.QPSEnvVar); ok && val != "" {
		qps, err := strconv.Atoi(val)
		require.NoError(t, err)
		p.QPS = qps
	}
	// Get the ingress external url of test app
	testAppURL := tr.Platform.AcquireAppExternalURL("testapp")
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	// Get the ingress external url of tester app
	testerAppURL := tr.Platform.AcquireAppExternalURL("tester")
	require.NotEmpty(t, testerAppURL, "tester app external URL must not be empty")

	// Check if tester app endpoint is available
	t.Logf("tester app url: %s", testerAppURL)
	_, err = utils.HTTPGetNTimes(testerAppURL, numHealthChecks)
	require.NoError(t, err)

	// Perform dapr test
	endpoint := fmt.Sprintf("http://testapp:3000/actors")
	p.TargetEndpoint = endpoint
	body, err := json.Marshal(&p)
	require.NoError(t, err)

	t.Logf("running dapr test with params: %s", body)
	daprResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testerAppURL), body)
	t.Log("checking err...")
	require.NoError(t, err)
	require.NotEmpty(t, daprResp)

	appUsage, err := tr.Platform.GetAppUsage("testapp")
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage("testapp")
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts("testapp")
	require.NoError(t, err)
	require.Equal(t, 0, restarts)

	t.Logf("dapr test results: %s", string(daprResp))
	t.Logf("target dapr app consumed %vm CPU and %vMb of Memory", appUsage.CPUm, appUsage.MemoryMb)
	t.Logf("target dapr sidecar consumed %vm CPU and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)
	t.Logf("target dapr app or sidecar restarted %v times", restarts)

	var daprResult perf.TestResult
	err = json.Unmarshal(daprResp, &daprResult)
	require.NoError(t, err)

	percentiles := map[int]string{1: "75th", 2: "90th"}

	for k, v := range percentiles {
		daprValue := daprResult.DurationHistogram.Percentiles[k].Value
		t.Logf("%s percentile: %sms", v, fmt.Sprintf("%.2f", daprValue))
	}

	require.True(t, appUsage.MemoryMb < maxAppMemMb)
	require.True(t, sidecarUsage.MemoryMb < maxSidecarMemMb)
}
