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

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("workflowtestdapr")
	utils.InitHTTPClient(true)

	// This test can be run outside of Kubernetes too
	// Run the workflow e2e app using, for example, the Dapr CLI:
	//   ASPNETCORE_URLS=http://*:3000 dapr run --app-id workflowsapp --resources-path ./resources -- dotnet run
	// Then run this test with the env var "WORKFLOW_APP_ENDPOINT" pointing to the address of the app. For example:
	//   WORKFLOW_APP_ENDPOINT=http://localhost:3000 DAPR_E2E_TEST="workflows" make test-clean test-e2e-all |& tee test.log
	if os.Getenv("WORKFLOW_APP_ENDPOINT") == "" {
		testApps := []kube.AppDescription{
			{
				AppName:           "workflowsapp",
				DaprEnabled:       true,
				ImageName:         "e2e-workflowsapp",
				Replicas:          1,
				IngressEnabled:    true,
				IngressPort:       3000,
				DaprMemoryLimit:   "200Mi",
				DaprMemoryRequest: "100Mi",
				AppMemoryLimit:    "200Mi",
				AppMemoryRequest:  "100Mi",
				AppPort:           -1,
			},
		}

		tr = runner.NewTestRunner("workflowsapp", testApps, nil, nil)
		os.Exit(tr.Start(m))
	} else {
		os.Exit(m.Run())
	}
}

func getAppEndpoint() string {
	if env := os.Getenv("WORKFLOW_APP_ENDPOINT"); env != "" {
		return env
	}

	return tr.Platform.AcquireAppExternalURL("workflowsapp")
}

func startTest(url string, instanceID string) error {
	postString := fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	// Start the workflow and check that it is running
	resp, err := utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Errorf("failure starting workflow: %w", err)
	}

	getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Errorf("failure getting info on workflow: %w", err)
	}
	if string(resp) != "Running" {
		return fmt.Errorf("expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	return nil
}

func pauseResumeTest(url string, instanceID string) error {
	postString := fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	// Start the workflow and check that it is running
	resp, err := utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Errorf("failure starting workflow: %w", err)
	}

	getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Errorf("failure getting info on started workflow: %w", err)
	}
	if string(resp) != "Running" {
		return fmt.Errorf("expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	postString = fmt.Sprintf("%s/PauseWorkflow/dapr/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Errorf("failure pausing workflow: %w", err)
	}

	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Errorf("failure getting info on paused workflow: %w", err)
	}
	if string(resp) != "Suspended" {
		return fmt.Errorf("expected workflow to be Suspended, actual workflow state is: %s", string(resp))
	}

	// Resume the workflow
	postString = fmt.Sprintf("%s/ResumeWorkflow/dapr/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Errorf("failure resuming workflow: %w", err)
	}

	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Errorf("failure getting info on resumed workflow: %w", err)
	}
	if string(resp) != "Running" {
		return fmt.Errorf("expected resumed workflow to be Running, actual workflow state is: %s", string(resp))
	}

	return nil
}

func raiseEventTest(url string, instanceID string) error {
	postString := fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	// Start the workflow and check that it is running
	resp, err := utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Errorf("failure starting workflow: %w", err)
	}

	getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Errorf("failure getting info on workflow: %w", err)
	}
	if string(resp) != "Running" {
		return fmt.Errorf("expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	// Raise an event on the workflow
	postString = fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ChangePurchaseItem/1", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)

	time.Sleep(1 * time.Second)

	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Errorf("failure getting info on workflow: %w", err)
	}
	if string(resp) != "Completed" {
		return fmt.Errorf("expected workflow to be Completed, actual workflow state is: %s", string(resp))
	}

	return nil
}

// Functions for each test case
func purgeTest(url string, instanceID string) error {
	// Start the workflow and check that it is running
	postString := fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	resp, err := utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Errorf("failure starting workflow: %w", err)
	}

	getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Errorf("failure getting info on newly started workflow: %w", err)
	}
	if string(resp) != "Running" {
		return fmt.Errorf("expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	// Terminate the workflow
	postString = fmt.Sprintf("%s/TerminateWorkflow/dapr/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Errorf("failure terminating workflow: %w", err)
	}
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Errorf("failure getting info on terminated workflow: %w", err)
	}
	if string(resp) != "Terminated" {
		return fmt.Errorf("expected workflow to be Terminated, actual workflow state is: %s", string(resp))
	}

	// Purge the workflow
	postString = fmt.Sprintf("%s/PurgeWorkflow/dapr/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Errorf("failure purging workflow: %w", err)
	}

	// Startup a new workflow with the same instanceID to ensure that it is available
	postString = fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Errorf("failure starting workflow: %w", err)
	}

	instanceID = string(resp)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Errorf("failure getting info on newly started workflow: %w", err)
	}
	if string(resp) != "Running" {
		return fmt.Errorf("expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	return nil
}

func TestWorkflow(t *testing.T) {
	// Get the ingress external url of test app
	externalURL := getAppEndpoint()
	require.NotEmpty(t, externalURL, "external URL must not be empty")

	// Check if test app endpoint is available
	require.NoError(t, utils.HealthCheckApps(externalURL))

	// Generate a unique test suffix for this test
	suffixBytes := make([]byte, 7)
	_, err := io.ReadFull(rand.Reader, suffixBytes)
	require.NoError(t, err)
	suffix := hex.EncodeToString(suffixBytes)

	// Run tests
	t.Run("Start", func(t *testing.T) {
		require.NoError(t, startTest(externalURL, "start-"+suffix))
	})

	t.Run("Pause and Resume", func(t *testing.T) {
		require.NoError(t, pauseResumeTest(externalURL, "pause-"+suffix))
	})

	t.Run("Purge", func(t *testing.T) {
		require.NoError(t, purgeTest(externalURL, "purge-"+suffix))
	})

	t.Run("Raise event", func(t *testing.T) {
		require.NoError(t, raiseEventTest(externalURL, "raiseEvent-"+suffix))
	})
}
