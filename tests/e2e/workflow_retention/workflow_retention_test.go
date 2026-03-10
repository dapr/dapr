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

package workflow_retention_e2e

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
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
	utils.SetupLogs("workflow_retention")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		{
			AppName:             "workflowsapp-retention",
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
			Config:              "workflowretentionconfig",
			DebugLoggingEnabled: true,
		},
	}

	tr = runner.NewTestRunner("workflow_retention", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

// TestWorkflowRetentionPolicy validates that the workflow state retention
// policy CRD with string duration fields (e.g. "10s") is correctly parsed
// in Kubernetes mode and that completed workflow instances are automatically
// purged after the configured retention period.
func TestWorkflowRetentionPolicy(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("workflowsapp-retention")
	require.NotEmpty(t, externalURL, "external URL must not be empty")
	require.NoError(t, utils.HealthCheckApps(externalURL))

	suffixBytes := make([]byte, 7)
	_, err := io.ReadFull(rand.Reader, suffixBytes)
	require.NoError(t, err)
	instanceID := "retention-" + hex.EncodeToString(suffixBytes)
	getString := fmt.Sprintf("%s/dapr/%s", externalURL, instanceID)

	// Start the workflow and check that it is running.
	_, err = utils.HTTPPost(
		fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", externalURL, instanceID), nil,
	)
	require.NoError(t, err, "failure starting workflow")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := utils.HTTPGet(getString)
		assert.NoError(c, err)
		assert.Equalf(c, "Running", string(resp),
			"expected Running, got: %s", string(resp))
	}, 10*time.Second, 100*time.Millisecond)

	// Raise all events to drive the workflow to completion.
	for _, event := range []string{
		"ChangePurchaseItem",
		"ConfirmSize",
		"ConfirmColor",
		"ConfirmAddress",
		"PayByCard",
	} {
		_, err = utils.HTTPPost(
			fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/%s/1", externalURL, instanceID, event), nil,
		)
		require.NoError(t, err, "failure raising event %s", event)
	}

	// Wait for the workflow to reach the Completed state.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := utils.HTTPGet(getString)
		assert.NoError(c, err)
		assert.Equalf(c, "Completed", string(resp),
			"expected Completed, got: %s", string(resp))
	}, 30*time.Second, 100*time.Millisecond)

	// The retention policy is configured with anyTerminal: "1s".
	// Wait for the workflow state to be automatically purged.
	// After purging, the GET should return empty/not-found.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, statusCode, err := utils.HTTPGetWithStatus(getString)
		assert.NoError(c, err)
		// A purged workflow returns 500 (internal error when instance not
		// found) or empty state, depending on the app implementation.
		// The key assertion: the workflow is no longer in "Completed" state.
		assert.NotEqualf(c, "Completed", string(resp),
			"workflow should have been purged by retention policy, but still returned: %s (status %d)",
			string(resp), statusCode)
	}, 60*time.Second, time.Second)
}
