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
	"encoding/json"
	"fmt"
	"os"
	"testing"

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
		"Terminated",
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
			// Trigger test
			body, err := json.Marshal(testCommandRequest{
				Message: "Hello Workflow.",
			})
			require.NoError(t, err)
			resp, err := utils.HTTPPost(fmt.Sprintf("%s/tests/%s", externalURL, tt.testCommand), body)
			require.NoError(t, err)
			var appResp appResponse
			err = json.Unmarshal(resp, &appResp)
			require.NoError(t, err)
			require.Equal(t, tt.expectedResponse, appResp.Message)
		})
	}
}
