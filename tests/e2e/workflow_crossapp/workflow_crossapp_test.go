//go:build e2e
// +build e2e

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

package workflow_crossapp_e2e

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	callerAppName = "wfxapp-caller"
	targetAppName = "wfxapp-target"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("workflow_crossapp")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		{
			AppName:             callerAppName,
			DaprEnabled:         true,
			ImageName:           "e2e-workflowsapp",
			Replicas:            1,
			IngressEnabled:      true,
			IngressPort:         3000,
			DaprMemoryLimit:     "200Mi",
			DaprMemoryRequest:   "100Mi",
			AppMemoryLimit:      "200Mi",
			AppMemoryRequest:    "100Mi",
			AppPort:             -1,
			DebugLoggingEnabled: true,
		},
		{
			AppName:             targetAppName,
			DaprEnabled:         true,
			ImageName:           "e2e-workflowsapp",
			Replicas:            1,
			IngressEnabled:      true,
			IngressPort:         3000,
			DaprMemoryLimit:     "200Mi",
			DaprMemoryRequest:   "100Mi",
			AppMemoryLimit:      "200Mi",
			AppMemoryRequest:    "100Mi",
			AppPort:             -1,
			DebugLoggingEnabled: true,
		},
	}

	tr = runner.NewTestRunner("workflow_crossapp", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

// TestWorkflowCrossAppOperations verifies that every client-level workflow
// operation can target a workflow instance hosted by another app: the caller
// app's sidecar drives start, get, pause, resume, raise event, terminate and
// purge against instances owned by the target app via the appID parameter on
// the workflow HTTP API.
func TestWorkflowCrossAppOperations(t *testing.T) {
	callerURL := tr.Platform.AcquireAppExternalURL(callerAppName)
	require.NotEmpty(t, callerURL, "caller external URL must not be empty")

	targetURL := tr.Platform.AcquireAppExternalURL(targetAppName)
	require.NotEmpty(t, targetURL, "target external URL must not be empty")

	require.NoError(t, utils.HealthCheckApps(callerURL, targetURL))

	// The workflow API returns 202 for accepted mutations; effects apply
	// asynchronously, so every status assertion polls the target instance.
	waitTargetStatus := func(t *testing.T, instanceID, wantStatus string) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			body, status, err := utils.HTTPGetWithStatus(
				fmt.Sprintf("%s/CrossAppGetWorkflow/dapr/%s/%s", callerURL, instanceID, targetAppName))
			assert.NoError(c, err)
			if !assert.Equalf(c, http.StatusOK, status, "get response body: %s", string(body)) {
				return
			}
			var state struct {
				RuntimeStatus string `json:"runtimeStatus"`
			}
			if !assert.NoError(c, json.Unmarshal(body, &state)) {
				return
			}
			assert.Equal(c, wantStatus, state.RuntimeStatus)
		}, 60*time.Second, 2*time.Second)
	}

	post := func(t *testing.T, url string, wantStatus int) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			body, status, err := utils.HTTPPostWithStatus(url, nil)
			assert.NoError(c, err)
			assert.Equalf(c, wantStatus, status, "response body: %s", string(body))
		}, 60*time.Second, 2*time.Second)
	}

	instanceID := "wfx-ops-" + randomID()

	t.Run("start on target app", func(t *testing.T) {
		post(t, fmt.Sprintf("%s/CrossAppStartWorkflow/dapr/WaitForFinish/%s/%s",
			callerURL, instanceID, targetAppName), http.StatusAccepted)
		waitTargetStatus(t, instanceID, "RUNNING")
	})

	t.Run("instance is hosted by the target, not the caller", func(t *testing.T) {
		// The target sees the instance through its own sidecar.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			body, status, err := utils.HTTPGetWithStatus(
				fmt.Sprintf("%s/CrossAppGetWorkflow/dapr/%s/%s", targetURL, instanceID, targetAppName))
			assert.NoError(c, err)
			assert.Equalf(c, http.StatusOK, status, "get response body: %s", string(body))
		}, 60*time.Second, 2*time.Second)

		// The caller's own sidecar does not host it.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			body, status, err := utils.HTTPGetWithStatus(
				fmt.Sprintf("%s/CrossAppGetWorkflow/dapr/%s/%s", callerURL, instanceID, callerAppName))
			assert.NoError(c, err)
			assert.Equalf(c, http.StatusNotFound, status, "get response body: %s", string(body))
		}, 60*time.Second, 2*time.Second)
	})

	t.Run("pause and resume", func(t *testing.T) {
		post(t, fmt.Sprintf("%s/CrossAppPauseWorkflow/dapr/%s/%s",
			callerURL, instanceID, targetAppName), http.StatusAccepted)
		waitTargetStatus(t, instanceID, "SUSPENDED")

		post(t, fmt.Sprintf("%s/CrossAppResumeWorkflow/dapr/%s/%s",
			callerURL, instanceID, targetAppName), http.StatusAccepted)
		waitTargetStatus(t, instanceID, "RUNNING")
	})

	t.Run("raise event completes the workflow", func(t *testing.T) {
		post(t, fmt.Sprintf("%s/CrossAppRaiseWorkflowEvent/dapr/%s/Finish/done/%s",
			callerURL, instanceID, targetAppName), http.StatusAccepted)
		waitTargetStatus(t, instanceID, "COMPLETED")
	})

	t.Run("purge removes the instance from the target", func(t *testing.T) {
		post(t, fmt.Sprintf("%s/CrossAppPurgeWorkflow/dapr/%s/%s",
			callerURL, instanceID, targetAppName), http.StatusAccepted)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			body, status, err := utils.HTTPGetWithStatus(
				fmt.Sprintf("%s/CrossAppGetWorkflow/dapr/%s/%s", callerURL, instanceID, targetAppName))
			assert.NoError(c, err)
			assert.Equalf(c, http.StatusNotFound, status, "get response body: %s", string(body))
		}, 60*time.Second, 2*time.Second)
	})

	t.Run("terminate", func(t *testing.T) {
		terminateID := "wfx-term-" + randomID()
		post(t, fmt.Sprintf("%s/CrossAppStartWorkflow/dapr/WaitForFinish/%s/%s",
			callerURL, terminateID, targetAppName), http.StatusAccepted)
		waitTargetStatus(t, terminateID, "RUNNING")

		post(t, fmt.Sprintf("%s/CrossAppTerminateWorkflow/dapr/%s/%s",
			callerURL, terminateID, targetAppName), http.StatusAccepted)
		waitTargetStatus(t, terminateID, "TERMINATED")
	})
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate random ID: " + err.Error())
	}
	return hex.EncodeToString(b)
}
