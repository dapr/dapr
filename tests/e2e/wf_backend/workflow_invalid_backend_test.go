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

package wf_backend_e2e

import (
	"os"
	"testing"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

var (
	tr            *runner.TestRunner
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
		validateInvalidWorkflowBackendSetup(m)

		// Exit with 0 to indicate success
		os.Exit(0)
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

func validateInvalidWorkflowBackendSetup(m *testing.M) {
	var testApps []kube.AppDescription
	testApps = append(testApps, getTestApp("multibackend"))
	testApps = append(testApps, getTestApp("invalidbackend"))
	comps := []kube.ComponentDescription{
		{
			Name:     "sqlitebackend2",
			TypeName: "workflowbackend.sqlite",
			MetaData: map[string]kube.MetadataValue{
				"connectionString": {Raw: `""`},
			},
			Scopes: []string{appNamePrefix + "-multibackend"},
		},
		{
			Name:     "actorbackend",
			TypeName: "workflowbackend.actor",
			Scopes:   []string{appNamePrefix + "-multibackend"},
		},
		{
			Name:     "invalidbackend",
			TypeName: "workflowbackend.invalidbackend",
			MetaData: map[string]kube.MetadataValue{
				"connectionString": {Raw: `""`},
			},
			Scopes: []string{appNamePrefix + "-invalidbackend"},
		},
	}

	tr = runner.NewTestRunner("workflowsapp-multibackend", testApps, comps, nil)
	exitCode := tr.Start(m)

	// If there are multiple workflow backend is defined, then app should not start
	if exitCode != 1 {
		// create panic when exitCode is not 1
		panic("Workflow with invalid backend setup should not start")
	}
}
