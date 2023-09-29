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

package actor_sdks_e2e

import (
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const (
	appName         = "actorinvocationapp" // App name in Dapr.
	numHealthChecks = 60                   // Number of get calls before starting tests.

	actorInvokeURLFormat = "%s/proxy/%s/%s/%s/%s" // URL to invoke a Dapr's actor method in test app.
)

var (
	tr   *runner.TestRunner
	apps []kube.AppDescription
)

func healthCheckApp(t *testing.T, externalURL string, numHealthChecks int) {
	t.Logf("Starting health check for %s\n", externalURL)
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)
	t.Logf("Endpoint is healthy: %s\n", externalURL)
}

func TestMain(m *testing.M) {
	utils.SetupLogs("actor_sdks")
	utils.InitHTTPClient(false)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	apps = []kube.AppDescription{
		{
			AppName:          "actordotnet",
			DaprEnabled:      true,
			ImageName:        "e2e-actordotnet",
			Replicas:         1,
			IngressEnabled:   true,
			MetricsEnabled:   true,
			AppMemoryLimit:   "500Mi",
			AppMemoryRequest: "200Mi",
		},
		{
			AppName:          "actorpython",
			DaprEnabled:      true,
			ImageName:        "e2e-actorpython",
			Replicas:         1,
			IngressEnabled:   true,
			MetricsEnabled:   true,
			AppMemoryLimit:   "200Mi",
			AppMemoryRequest: "100Mi",
		},
		{
			AppName:        "actortestclient",
			DaprEnabled:    true,
			ImageName:      "e2e-actorclientapp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			DaprCPULimit:   "2.0",
			DaprCPURequest: "0.1",
			AppCPULimit:    "2.0",
			AppCPURequest:  "0.1",
		},
	}

	if utils.TestTargetOS() != "windows" {
		apps = append(apps,
			// Disables Java test on Windows due to poor support for Java on Windows containers.
			kube.AppDescription{
				AppName:          "actorjava",
				DaprEnabled:      true,
				ImageName:        "e2e-actorjava",
				Replicas:         1,
				IngressEnabled:   true,
				MetricsEnabled:   true,
				AppMemoryLimit:   "500Mi",
				AppMemoryRequest: "200Mi",
			},
			// Disables PHP test for Windows temporarily due to issues with its Windows container.
			// See https://github.com/dapr/dapr/issues/2953
			kube.AppDescription{
				AppName:          "actorphp",
				DaprEnabled:      true,
				ImageName:        "e2e-actorphp",
				Replicas:         1,
				IngressEnabled:   true,
				MetricsEnabled:   true,
				AppMemoryLimit:   "200Mi",
				AppMemoryRequest: "100Mi",
			})
	}

	tr = runner.NewTestRunner(appName, apps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorInvocationToSDK(t *testing.T) {
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	for _, appSpec := range apps {
		app := appSpec.AppName
		externalURL := tr.Platform.AcquireAppExternalURL(app)
		healthCheckApp(t, externalURL, numHealthChecks)
	}

	actorTypes := []string{"DotNetCarActor"}
	externalURL := tr.Platform.AcquireAppExternalURL("actortestclient")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err, "error while waiting for app actortestclient")

	for _, actorType := range actorTypes {
		t.Run("Actor remote invocation with exception on "+actorType, func(t *testing.T) {
			actorID := uuid.NewString()
			errorMessage := "random error message - " + uuid.NewString()

			body, statusCode, headers, err := utils.HTTPPostComplete(
				fmt.Sprintf(actorInvokeURLFormat, externalURL, actorType, actorID, "method", "throwException"), []byte(errorMessage))
			log.Printf("SDK exception call statusCode: %v", statusCode)
			log.Printf("SDK exception call res: %v", string(body))
			log.Printf("SDK exception call err: %s", err)
			if err != nil {
				log.Printf("failed to invoke method testmethod. Error='%v' Response='%s'", err, string(body))
			}

			require.NoError(t, err, "failed to invoke actor method with SDK exception")
			_, ok := headers["X-Daprerrorresponseheader"]
			require.True(t, ok, "did not find X-Daprerrorresponseheader")
			require.True(t, strings.Contains(string(body), errorMessage))
		})
	}
}

func TestActorInvocationCrossSDKs(t *testing.T) {
	actorTypes := []string{"DotNetCarActor", "PythonCarActor"}
	if utils.TestTargetOS() != "windows" {
		actorTypes = append(actorTypes,
			// Disables Java test on Windows due to poor support for Java on Windows containers.
			"JavaCarActor",
			// Disables PHP test for Windows temporarily due to issues with its Windows container.
			// See https://github.com/dapr/dapr/issues/2953
			"PHPCarActor",
		)
	}

	scenarios := []struct {
		method           string
		payload          string
		expectedResponse string
	}{
		{
			"incrementAndGet/%s/%s",
			"",
			"1",
		},
		{
			"carFromJSON/%s/%s",
			`{
				"vin": "JTXXX923X71194343",
				"maker": "carmaker",
				"model": "model123",
				"trim": "basic",
				"modelYear": 1960,
				"buildYear": 1959,
				"photo": "R0lGODdhAgACAJEAAAAAAP///wAAAAAAACH5BAkAAAIALAAAAAACAAIAAAICjFMAOw=="
		  }`,
			`{
				"vin": "JTXXX923X71194343",
				"maker": "carmaker",
				"model": "model123",
				"trim": "basic",
				"modelYear": 1960,
				"buildYear": 1959,
				"photo": "R0lGODdhAgACAJEAAAAAAP///wAAAAAAACH5BAkAAAIALAAAAAACAAIAAAICjFMAOw=="
			}`,
		},
		{
			"carToJSON/%s/%s",
			`{
				"vin": "JTXXX923X71194343",
				"maker": "carmaker",
				"model": "model123",
				"trim": "basic",
				"modelYear": 1960,
				"buildYear": 1959,
				"photo": "R0lGODdhAgACAJEAAAAAAP///wAAAAAAACH5BAkAAAIALAAAAAACAAIAAAICjFMAOw=="
		  }`,
			`{
				"vin": "JTXXX923X71194343",
				"maker": "carmaker",
				"model": "model123",
				"trim": "basic",
				"modelYear": 1960,
				"buildYear": 1959,
				"photo": "R0lGODdhAgACAJEAAAAAAP///wAAAAAAACH5BAkAAAIALAAAAAACAAIAAAICjFMAOw=="
			}`,
		},
	}

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	for _, appSpec := range apps {
		app := appSpec.AppName
		externalURL := tr.Platform.AcquireAppExternalURL(app)
		healthCheckApp(t, externalURL, numHealthChecks)
	}

	t.Log("Sleeping for 10 seconds ...")
	time.Sleep(10 * time.Second)

	for _, appSpec := range apps {
		app := appSpec.AppName
		if app == "actortestclient" {
			// This app does not use any SDK, so we skip it.
			continue
		}

		t.Logf("Getting URL for app %s ...", app)
		externalURL := tr.Platform.AcquireAppExternalURL(app)

		for _, actorType := range actorTypes {
			for _, tt := range scenarios {
				method := fmt.Sprintf(tt.method, actorType, uuid.New().String())
				name := fmt.Sprintf("Test %s calling %s", app, fmt.Sprintf(tt.method, actorType, "ActorId"))
				t.Run(name, func(t *testing.T) {
					t.Logf("invoking %s/%s", externalURL, method)
					resp, err := utils.HTTPPost(fmt.Sprintf("%s/%s", externalURL, method), []byte(tt.payload))
					t.Log("checking err...")
					require.NoError(t, err)

					appResponse := string(resp)
					t.Logf("response: %s\n", appResponse)
					require.JSONEq(t, tt.expectedResponse, appResponse)
				})
			}
		}
	}
}
