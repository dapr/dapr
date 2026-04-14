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

package workflow_accesspolicy_e2e

import (
	"crypto/rand"
	"encoding/hex"
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

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("workflow_accesspolicy")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		{
			AppName:             "wfacl-caller",
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
			Config:              "wfaclconfig",
		},
		{
			AppName:             "wfacl-target",
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
			Config:              "wfaclconfig",
		},
	}

	tr = runner.NewTestRunner("workflow_accesspolicy", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestWorkflowAccessPolicy(t *testing.T) {
	callerURL := tr.Platform.AcquireAppExternalURL("wfacl-caller")
	require.NotEmpty(t, callerURL, "wfacl-caller external URL must not be empty")

	targetURL := tr.Platform.AcquireAppExternalURL("wfacl-target")
	require.NotEmpty(t, targetURL, "wfacl-target external URL must not be empty")

	require.NoError(t, utils.HealthCheckApps(callerURL, targetURL))

	t.Run("allowed workflow succeeds via Dapr HTTP API", func(t *testing.T) {
		instanceID := "allowed-" + randomID()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, status, err := utils.HTTPPostWithStatus(
				fmt.Sprintf("%s/StartWorkflow/dapr/AllowedWorkflow/%s", callerURL, instanceID),
				nil,
			)
			assert.NoError(c, err)
			assert.Equal(c, http.StatusOK, status, string(resp))
		}, 60*time.Second, 2*time.Second)
	})

	t.Run("denied workflow fails via Dapr HTTP API", func(t *testing.T) {
		instanceID := "denied-" + randomID()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, status, err := utils.HTTPPostWithStatus(
				fmt.Sprintf("%s/StartWorkflow/dapr/DeniedWorkflow/%s", callerURL, instanceID),
				nil,
			)
			assert.NoError(c, err)
			assert.Equal(c, http.StatusInternalServerError, status)
		}, 60*time.Second, 2*time.Second)
	})

	t.Run("unmentioned workflow fails with default deny", func(t *testing.T) {
		instanceID := "unmentioned-" + randomID()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, status, err := utils.HTTPPostWithStatus(
				fmt.Sprintf("%s/StartWorkflow/dapr/PlaceOrder/%s", callerURL, instanceID),
				nil,
			)
			assert.NoError(c, err)
			assert.Equal(c, http.StatusInternalServerError, status)
		}, 60*time.Second, 2*time.Second)
	})

	t.Run("target can start its own workflows", func(t *testing.T) {
		instanceID := "selfcall-" + randomID()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, status, err := utils.HTTPPostWithStatus(
				fmt.Sprintf("%s/StartWorkflow/dapr/AllowedWorkflow/%s", targetURL, instanceID),
				nil,
			)
			assert.NoError(c, err)
			assert.Equal(c, http.StatusOK, status, string(resp))
		}, 60*time.Second, 2*time.Second)
	})
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate random ID: " + err.Error())
	}
	return hex.EncodeToString(b)
}
