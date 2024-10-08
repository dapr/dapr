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
	"os"
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
			AppName:             "actordotnet",
			DaprEnabled:         true,
			ImageName:           "e2e-actordotnet",
			DebugLoggingEnabled: true,
			Config:              "omithealthchecksconfig",
			Replicas:            1,
			IngressEnabled:      true,
			MetricsEnabled:      true,
			AppMemoryLimit:      "500Mi",
			AppMemoryRequest:    "200Mi",
		},
		{
			AppName:             "actorpython",
			DaprEnabled:         true,
			ImageName:           "e2e-actorpython",
			DebugLoggingEnabled: true,
			Config:              "omithealthchecksconfig",
			Replicas:            1,
			IngressEnabled:      true,
			MetricsEnabled:      true,
			AppMemoryLimit:      "200Mi",
			AppMemoryRequest:    "100Mi",
		},
	}

	if utils.TestTargetOS() != "windows" {
		apps = append(apps,
			// Disables Java test on Windows due to poor support for Java on Windows containers.
			kube.AppDescription{
				AppName:             "actorjava",
				DaprEnabled:         true,
				ImageName:           "e2e-actorjava",
				DebugLoggingEnabled: true,
				Config:              "omithealthchecksconfig",
				Replicas:            1,
				IngressEnabled:      true,
				MetricsEnabled:      true,
				AppMemoryLimit:      "500Mi",
				AppMemoryRequest:    "200Mi",
			},
			// Disables PHP test for Windows temporarily due to issues with its Windows container.
			// See https://github.com/dapr/dapr/issues/2953
			kube.AppDescription{
				AppName:             "actorphp",
				DaprEnabled:         true,
				ImageName:           "e2e-actorphp",
				DebugLoggingEnabled: true,
				Config:              "omithealthchecksconfig",
				Replicas:            1,
				IngressEnabled:      true,
				MetricsEnabled:      true,
				AppMemoryLimit:      "200Mi",
				AppMemoryRequest:    "100Mi",
			})
	}

	tr = runner.NewTestRunner(appName, apps, nil, nil)
	os.Exit(tr.Start(m))
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

	t.Log("Sleeping for 15 seconds ...")
	time.Sleep(15 * time.Second)

	for _, appSpec := range apps {
		app := appSpec.AppName
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
