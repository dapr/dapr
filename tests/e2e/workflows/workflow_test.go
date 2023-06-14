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

type testCommandRequest struct {
	Message string `json:"message,omitempty"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

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

var workflowAppTests = []struct {
	in               string
	app              string
	testCommand      string
	expectedResponse string
}{
	{
		"workflow dapr",
		"workflowsapp",
		"start",
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

			postString := fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/e2eInstanceId", externalURL)

			// Sleep so that the workflow engine starts
			time.Sleep(10 * time.Second)

			// Start the workflow
			resp, err := utils.HTTPPost(postString, nil)
			require.NoError(t, err)

			// Assert that the workflow instanceID is returned
			require.Equal(t, "e2eInstanceId", string(resp))

			getString := fmt.Sprintf("%s/dapr/e2eInstanceId", externalURL)
			resp, err = utils.HTTPGet(getString)

			// Assert that the status returned is Running
			require.Equal(t, "Running", string(resp))

			// Pause the workflow
			postString = fmt.Sprintf("%s/PauseWorkflow/dapr/e2eInstanceId", externalURL)
			resp, err = utils.HTTPPost(postString, nil)

			resp, err = utils.HTTPGet(getString)
			require.Equal(t, "Suspended", string(resp))

			// Resume the workflow
			postString = fmt.Sprintf("%s/ResumeWorkflow/dapr/e2eInstanceId", externalURL)
			resp, err = utils.HTTPPost(postString, nil)

			resp, err = utils.HTTPGet(getString)
			require.Equal(t, "Running", string(resp))

			// Raise an event on the workflow
			postString = fmt.Sprintf("%s/RaiseWorkflowEvent/dapr/e2eInstanceId/ChangePurchaseItem/1", externalURL)
			resp, err = utils.HTTPPost(postString, nil)

			time.Sleep(1 * time.Second)

			resp, err = utils.HTTPGet(getString)
			require.Equal(t, "Completed", string(resp))

			// Purge the workflow
			postString = fmt.Sprintf("%s/PaurgeWorkflow/dapr/e2eInstanceId", externalURL)
			resp, err = utils.HTTPPost(postString, nil)

			// Start another workflow with the same instanceID to ensure that the previous one no longer exists
			// Assert that the workflow instanceID is returned
			postString = fmt.Sprintf("%s/StartWorkflow/dapr/placeOrder/e2eInstanceId", externalURL)
			resp, err = utils.HTTPPost(postString, nil)
			require.NoError(t, err)
			require.Equal(t, "e2eInstanceId", string(resp))

			// Terminate the workflow
			postString = fmt.Sprintf("%s/TerminateWorkflow/dapr/e2eInstanceId", externalURL)
			resp, err = utils.HTTPPost(postString, nil)
			require.NoError(t, err)

			resp, err = utils.HTTPGet(getString)
			require.Equal(t, "Terminated", string(resp))

			// Purge the workflow
			postString = fmt.Sprintf("%s/PaurgeWorkflow/dapr/e2eInstanceId", externalURL)
			resp, err = utils.HTTPPost(postString, nil)
			require.NoError(t, err)

		})
	}
}
