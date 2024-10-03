//go:build e2e
// +build e2e

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

package workflows_e2e

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

var (
	tr            *runner.TestRunner
	backends      = []string{"actors", "sqlite"}
	appNamePrefix = "workflowsapp"
)

func TestMain(m *testing.M) {
	utils.SetupLogs("workflowtestdapr")
	utils.InitHTTPClient(true)

	// This test can be run outside of Kubernetes too
	// Run the workflow e2e app using, for example, the Dapr CLI:
	//   ASPNETCORE_URLS=http://*:3000 dapr run --app-id workflowsapp --resources-path ./resources -- dotnet run
	// Then run this test with the env var "WORKFLOW_APP_ENDPOINT" pointing to the address of the app. For example:
	//   WORKFLOW_APP_ENDPOINT=http://localhost:3000 DAPR_E2E_TEST="workflows" make test-clean test-e2e-all |& tee test.log
	if os.Getenv("WORKFLOW_APP_ENDPOINT") == "" {
		// Set the configuration as environment variables for the test app.
		var testApps []kube.AppDescription
		for _, backend := range backends {
			testApps = append(testApps, getTestApp(backend))
		}

		comps := []kube.ComponentDescription{
			{
				Name:     "sqlitebackend",
				TypeName: "workflowbackend.sqlite",
				MetaData: map[string]kube.MetadataValue{
					"connectionString": {Raw: `""`},
				},
				Scopes: []string{appNamePrefix + "-sqlite"},
			},
		}

		tr = runner.NewTestRunner("workflowsapp", testApps, comps, nil)
		os.Exit(tr.Start(m))
	} else {
		os.Exit(m.Run())
	}
}

func getTestApp(backend string) kube.AppDescription {
	testApps := kube.AppDescription{
		AppName:             appNamePrefix + "-" + backend,
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
	}

	return testApps
}

func getAppEndpoint(testAppName string) string {
	if env := os.Getenv("WORKFLOW_APP_ENDPOINT"); env != "" {
		return env
	}

	return tr.Platform.AcquireAppExternalURL(testAppName)
}

func startTest(url string, instanceID string) func(t *testing.T) {
	return func(t *testing.T) {
		getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)

		// Start the workflow and check that it is running
		resp, err := utils.HTTPPost(fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID), nil)
		require.NoError(t, err, "failure starting workflow")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Running", string(resp), "expected workflow to be Running, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)
	}
}

func pauseResumeTest(url string, instanceID string) func(t *testing.T) {
	return func(t *testing.T) {
		getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)

		// Start the workflow and check that it is running
		resp, err := utils.HTTPPost(fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID), nil)
		require.NoError(t, err, "failure starting workflow")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Running", string(resp), "expected workflow to be Running, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)

		// Raise an event on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ChangePurchaseItem/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		// Raise parallel events on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmSize/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmColor/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmAddress/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		// Pause the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/PauseWorkflow/dapr/%s", url, instanceID), nil)
		require.NoError(t, err, "failure pausing workflow")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Suspended", string(resp), "expected workflow to be Suspended, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)

		// Resume the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/ResumeWorkflow/dapr/%s", url, instanceID), nil)
		require.NoError(t, err, "failure resuming workflow")

		// Raise a parallel event on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/PayByCard/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		time.Sleep(5 * time.Second)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Running", string(resp), "expected workflow to be Running, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)
	}
}

func raiseEventTest(url string, instanceID string) func(t *testing.T) {
	return func(t *testing.T) {
		getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)

		// Start the workflow and check that it is running
		resp, err := utils.HTTPPost(fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID), nil)
		require.NoError(t, err, "failure starting workflow")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Running", string(resp), "expected workflow to be Running, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)

		// Raise an event on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ChangePurchaseItem/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		// Raise parallel events on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmSize/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmColor/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmAddress/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		// Raise a parallel event on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/PayByCard/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		time.Sleep(10 * time.Second)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Completed", string(resp), "expected workflow to be Completed, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)
	}
}

// Functions for each test case
func purgeTest(url string, instanceID string) func(t *testing.T) {
	return func(t *testing.T) {
		getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)

		// Start the workflow and check that it is running
		resp, err := utils.HTTPPost(fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID), nil)
		require.NoError(t, err, "failure starting workflow")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Running", string(resp), "expected workflow to be Running, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)

		// Raise an event on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ChangePurchaseItem/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		// Raise parallel events on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmSize/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmColor/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmAddress/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		// Terminate the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/TerminateWorkflow/dapr/%s", url, instanceID), nil)
		require.NoError(t, err, "failure terminating workflow")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Terminated", string(resp), "expected workflow to be Terminated, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)

		// Purge the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/PurgeWorkflow/dapr/%s", url, instanceID), nil)
		require.NoError(t, err, "failure purging workflow")

		// Start a new workflow with the same instanceID to ensure that it is available
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID), nil)
		require.NoError(t, err, "failure starting workflow")

		require.Equal(t, instanceID, string(resp))

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Running", string(resp), "expected workflow to be Running, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)

		// Raise a parallel event on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/PayByCard/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		time.Sleep(5 * time.Second)

		// Purge will clear the instance data. There are insufficient triggered events which lead to the failure.
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			require.NoError(t, err, "failure getting info on workflow")
			assert.NotEqualf(t, "Completed", string(resp), "expected workflow not to be Completed, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)
	}
}

// Functions for each test case
func monitorTest(url string, instanceID string) func(t *testing.T) {
	return func(t *testing.T) {
		getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)

		// Start the workflow and check that it is running
		resp, err := utils.HTTPPost(fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID), nil)
		require.NoError(t, err, "failure starting workflow")

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			assert.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Running", string(resp), "expected workflow to be Running, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)

		// Start the monitor workflow
		monitorInstanceID := "m-" + instanceID
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/StartMonitorWorkflow/dapr/%s/%s", url, instanceID, monitorInstanceID), nil)

		require.NoError(t, err, "failure starting workflow")

		getMonitorString := fmt.Sprintf("%s/dapr/%s", url, monitorInstanceID)
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getMonitorString)
			assert.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Running", string(resp), "expected workflow to be Running, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)

		// Raise an event on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ChangePurchaseItem/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		// Raise parallel events on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmSize/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmColor/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ConfirmAddress/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		// Raise a parallel event on the workflow
		resp, err = utils.HTTPPost(fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/PayByCard/1", url, instanceID), nil)
		require.NoError(t, err, "failure raising event on workflow")

		time.Sleep(15 * time.Second)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getString)
			assert.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Completed", string(resp), "expected workflow to be Completed, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			resp, err = utils.HTTPGet(getMonitorString)
			assert.NoError(t, err, "failure getting info on workflow")
			assert.Equalf(t, "Completed", string(resp), "expected workflow to be Completed, actual workflow state is: %s", string(resp))
		}, 5*time.Second, 100*time.Millisecond)
	}
}

func TestWorkflow(t *testing.T) {
	for _, backend := range backends {
		t.Run(backend, func(t *testing.T) {
			// Get the ingress external url of test app
			externalURL := getAppEndpoint(appNamePrefix + "-" + backend)
			require.NotEmpty(t, externalURL, "external URL must not be empty")

			// Check if test app endpoint is available
			require.NoError(t, utils.HealthCheckApps(externalURL))

			// Generate a unique test suffix for this test
			suffixBytes := make([]byte, 7)
			_, err := io.ReadFull(rand.Reader, suffixBytes)
			require.NoError(t, err)
			suffix := hex.EncodeToString(suffixBytes)

			// Run tests
			t.Run("Start", startTest(externalURL, "start-"+suffix))
			t.Run("Pause and Resume", pauseResumeTest(externalURL, "pause-"+suffix))
			t.Run("Purge", purgeTest(externalURL, "purge-"+suffix))
			t.Run("Raise event", raiseEventTest(externalURL, "raiseEvent-"+suffix))
			t.Run("Start monitor", monitorTest(externalURL, "monitor-"+suffix))
		})
	}
}
