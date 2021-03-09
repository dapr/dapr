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
	appName         = "actorinvocationapp"      // App name in Dapr.
	numHealthChecks = 60                        // Number of get calls before starting tests.
	callActorURL    = "%s/test/callActorMethod" // URL to force Actor registration
)

type actorCallRequest struct {
	ActorType       string `json:"actorType"`
	ActorId         string `json:"actorId"`
	Method          string `json:"method"`
	RemoteActorID   string `json:"remoteId,omitempty"`
	RemoteActorType string `json:"remoteType,omitempty"`
}

var tr *runner.TestRunner
var apps []kube.AppDescription

func getExternalURL(t *testing.T, appName string) string {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	return externalURL
}

func healthCheckApp(t *testing.T, externalURL string, numHealthChecks int) {
	t.Logf("Starting health check for %s\n", externalURL)
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)
	t.Logf("Endpoint is healthy: %s\n", externalURL)
}

func TestMain(m *testing.M) {
	// Disables this test for Windows temporarily due to issues with Windows containers.
	// Technically, this test can still work on Windows against K8s on Linux.
	// See https://github.com/dapr/dapr/issues/2695
	if runtime.GOOS == "windows" {
		return
	}

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	apps = []kube.AppDescription{
		{
			AppName:        "actorjava",
			DaprEnabled:    true,
			ImageName:      "e2e-actorjava",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "actordotnet",
			DaprEnabled:    true,
			ImageName:      "e2e-actordotnet",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "actorpython",
			DaprEnabled:    true,
			ImageName:      "e2e-actorpython",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "actorphp",
			DaprEnabled:    true,
			ImageName:      "e2e-actorphp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	tr = runner.NewTestRunner(appName, apps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorInvocationCrossSDKs(t *testing.T) {
	actorTypes := []string{"DotNetCarActor", "JavaCarActor", "PythonCarActor", "PHPCarActor"}
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

	time.Sleep(10 * time.Second)

	for _, appSpec := range apps {
		app := appSpec.AppName
		externalURL := tr.Platform.AcquireAppExternalURL(app)

		for _, actorType := range actorTypes {
			for _, tt := range scenarios {
				method := fmt.Sprintf(tt.method, actorType, uuid.New().String())
				name := fmt.Sprintf("Test %s calling %s", app, method)
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
