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
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const numHealthChecks = 60 // Number of times to check for endpoint health per app.

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("workflowtestdapr")
	utils.InitHTTPClient(true)

	// This test shows how to validate the side-car injection
	// and validate the response by using test app's service endpoint

	// These apps will be deployed for workflowdapr test before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:           "workflowsapp",
			DaprEnabled:       true,
			ImageName:         "e2e-workflowsapp",
			Replicas:          1,
			IngressEnabled:    true,
			MetricsEnabled:    true,
			DaprMemoryLimit:   "200Mi",
			DaprMemoryRequest: "100Mi",
			AppMemoryLimit:    "200Mi",
			AppMemoryRequest:  "100Mi",
			AppPort:           3000,
		},
	}

	tr = runner.NewTestRunner("workflowsapp", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func startTest(url string, instanceID string) string {
	postString := fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	// Start the workflow and check that it is running
	resp, err := utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Sprintf("Failure starting workflow: %s", err.Error())
	}

	getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Sprintf("Failure getting info on workflow: %s", err.Error())
	}
	if string(resp) != "Running" {
		return fmt.Sprintf("Expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	return string(resp)
}

func pauseResumeTest(url string, instanceID string) string {
	postString := fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	// Start the workflow and check that it is running
	resp, err := utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Sprintf("Failure starting workflow: %s", err.Error())
	}

	getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Sprintf("Failure getting info on started workflow: %s", err.Error())
	}
	if string(resp) != "Running" {
		return fmt.Sprintf("Expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	postString = fmt.Sprintf("%s/PauseWorkflow/dapr/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Sprintf("Failure pausing workflow: %s", err.Error())
	}

	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Sprintf("Failure getting info on paused workflow: %s", err.Error())
	}
	if string(resp) != "Suspended" {
		return fmt.Sprintf("Expected workflow to be Suspended, actual workflow state is: %s", string(resp))
	}

	// Resume the workflow
	postString = fmt.Sprintf("%s/ResumeWorkflow/dapr/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Sprintf("Failure resuming workflow: %s", err.Error())
	}

	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Sprintf("Failure getting info on resumed workflow: %s", err.Error())
	}
	if string(resp) != "Running" {
		return fmt.Sprintf("Expected resumed workflow to be Running, actual workflow state is: %s", string(resp))
	}

	return "Success"
}

func raiseEventTest(url string, instanceID string) string {
	postString := fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	// Start the workflow and check that it is running
	resp, err := utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Sprintf("Failure starting workflow: %s", err.Error())
	}

	getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Sprintf("Failure getting info on workflow: %s", err.Error())
	}
	if string(resp) != "Running" {
		return fmt.Sprintf("Expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	// Raise an event on the workflow
	postString = fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/%s/ChangePurchaseItem/1", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)

	time.Sleep(1 * time.Second)

	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Sprintf("Failure getting info on workflow: %s", err.Error())
	}
	if string(resp) != "Completed" {
		return fmt.Sprintf("Expected workflow to be Completed, actual workflow state is: %s", string(resp))
	}

	return string(resp)
}

// Functions for each test case
func purgeTest(url string, instanceID string) string {
	// Start the workflow and check that it is running
	postString := fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	resp, err := utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Sprintf("Failure starting workflow: %s", err.Error())
	}

	getString := fmt.Sprintf("%s/dapr/%s", url, instanceID)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Sprintf("Failure getting info on newly started workflow: %s", err.Error())
	}
	if string(resp) != "Running" {
		return fmt.Sprintf("Expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	// Terminate the workflow
	postString = fmt.Sprintf("%s/TerminateWorkflow/dapr/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Sprintf("Failure terminating workflow: %s", err.Error())
	}
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Sprintf("Failure getting info on terminated workflow: %s", err.Error())
	}
	if string(resp) != "Terminated" {
		return fmt.Sprintf("Expected workflow to be Terminated, actual workflow state is: %s", string(resp))
	}

	// Purge the workflow
	postString = fmt.Sprintf("%s/PurgeWorkflow/dapr/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Sprintf("Failure purging workflow: %s", err.Error())
	}

	// Startup a new workflow with the same instanceID to ensure that it is available
	postString = fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/%s", url, instanceID)
	resp, err = utils.HTTPPost(postString, nil)
	if err != nil {
		return fmt.Sprintf("Failure starting workflow: %s", err.Error())
	}

	instanceID = string(resp)
	resp, err = utils.HTTPGet(getString)
	if err != nil {
		return fmt.Sprintf("Failure getting info on newly started workflow: %s", err.Error())
	}
	if string(resp) != "Running" {
		return fmt.Sprintf("Expected workflow to be Running, actual workflow state is: %s", string(resp))
	}

	return string(resp)
}

var workflowAppTests = []struct {
	in               string
	instanceID       string
	app              string
	expectedResponse string
}{
	{
		"start",
		"startID",
		"workflowsapp",
		"Running",
	},

	{
		"pauseResume",
		"pauseID",
		"workflowsapp",
		"Success",
	},

	{
		"purge",
		"purgeID",
		"workflowsapp",
		"Running",
	},

	{
		"raiseEvent",
		"raiseEventID",
		"workflowsapp",
		"Completed",
	},
}

func TestWorkflow(t *testing.T) {
	for _, tt := range workflowAppTests {
		t.Run(tt.in, func(t *testing.T) {
			// Get the ingress external url of test app
			externalURL := tr.Platform.AcquireAppExternalURL(tt.app)
			require.NotEmpty(t, externalURL, "external URL must not be empty")

			// Check if test app endpoint is available
			_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
			require.NoError(t, err)

			result := "false"
			switch tt.in {
			case "purge":
				result = purgeTest(externalURL, tt.instanceID)
			case "start":
				result = startTest(externalURL, tt.instanceID)
			case "pauseResume":
				result = pauseResumeTest(externalURL, tt.instanceID)
			case "raiseEvent":
				result = raiseEventTest(externalURL, tt.instanceID)
			}

			require.Equal(t, tt.expectedResponse, result)
		})
	}
}
