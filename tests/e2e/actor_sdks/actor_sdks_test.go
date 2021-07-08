// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actor_sdks_e2e

import (
	"fmt"
	"os"
	"runtime"
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

var tr *runner.TestRunner
var apps []kube.AppDescription

func healthCheckApp(t *testing.T, externalURL string, numHealthChecks int) {
	t.Logf("Starting health check for %s\n", externalURL)
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)
	t.Logf("Endpoint is healthy: %s\n", externalURL)
}

func TestMain(m *testing.M) {
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	apps = []kube.AppDescription{
		{
			AppName:          "actorjava",
			DaprEnabled:      true,
			ImageName:        "e2e-actorjava",
			Replicas:         1,
			IngressEnabled:   true,
			MetricsEnabled:   true,
			AppMemoryLimit:   "500Mi",
			AppMemoryRequest: "200Mi",
		},
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
	}

	// Disables PHP test for Windows temporarily due to issues with its Windows container.
	// See https://github.com/dapr/dapr/issues/2953
	if runtime.GOOS != "windows" {
		apps = append(apps,
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

func TestActorInvocationCrossSDKs(t *testing.T) {
	actorTypes := []string{"DotNetCarActor", "JavaCarActor", "PythonCarActor"}
	// Disables PHP test for Windows temporarily due to issues with its Windows container.
	// See https://github.com/dapr/dapr/issues/2953
	if runtime.GOOS != "windows" {
		actorTypes = append(actorTypes, "PHPCarActor")
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
		t.Logf("Getting URL for app %s ...", app)
		externalURL := tr.Platform.AcquireAppExternalURL(app)

		for _, actorType := range actorTypes {
			for _, tt := range scenarios {
				method := fmt.Sprintf(tt.method, actorType, uuid.New().String())
				name := fmt.Sprintf("Test %s calling %s", app, fmt.Sprintf(tt.method, actorType, "ActorId"))
				t.Run(name, func(t *testing.T) {
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
