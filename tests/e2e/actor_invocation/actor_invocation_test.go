// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actor_invocation_e2e

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

const (
	appName         = "actorinvocationapp"      // App name in Dapr.
	numHealthChecks = 60                        // Number of get calls before starting tests.
	callActorURL    = "%s/test/callActorMethod" // URL to force Actor registration
)

type actorCallRequest struct {
	ActorType       string `json:"actorType"`
	ActorID         string `json:"actorId"`
	Method          string `json:"method"`
	RemoteActorID   string `json:"remoteId,omitempty"`
	RemoteActorType string `json:"remoteType,omitempty"`
}

var tr *runner.TestRunner

func getExternalURL(t *testing.T, appName string) string {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")
	return externalURL
}

func healthCheckApp(t *testing.T, externalURL string, numHealthChecks int) {
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)
}

func TestMain(m *testing.M) {
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "actor1",
			DaprEnabled:    true,
			ImageName:      "e2e-actorinvocationapp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
		{
			AppName:        "actor2",
			DaprEnabled:    true,
			ImageName:      "e2e-actorinvocationapp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorInvocation(t *testing.T) {
	firstActorURL := getExternalURL(t, "actor1")
	secondActorURL := getExternalURL(t, "actor2")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	healthCheckApp(t, firstActorURL, numHealthChecks)
	healthCheckApp(t, secondActorURL, numHealthChecks)

	// This basic test is already covered in actor_feature_tests but serves as a sanity check here to ensure apps are up.
	t.Run("Actor remote invocation", func(t *testing.T) {
		request := actorCallRequest{
			ActorType: "actor1",
			ActorID:   "10",
			Method:    "logCall",
		}

		body, _ := json.Marshal(request)

		_, status, err := utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, firstActorURL), body)
		require.NoError(t, err)
		require.Equal(t, 200, status)

		request = actorCallRequest{
			ActorType: "actor2",
			ActorID:   "20",
			Method:    "logCall",
		}

		body, _ = json.Marshal(request)

		_, status, err = utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, secondActorURL), body)
		require.NoError(t, err)
		require.Equal(t, 200, status)
	})

	t.Run("Actor cross actor call (same pod)", func(t *testing.T) {
		// Register the 2nd actor on the same pod.
		request := actorCallRequest{
			ActorType: "actor1",
			ActorID:   "11",
			Method:    "logCall",
		}

		body, _ := json.Marshal(request)

		_, status, err := utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, firstActorURL), body)
		require.NoError(t, err)
		require.Equal(t, 200, status)

		request = actorCallRequest{
			ActorType:       "actor1",
			ActorID:         "10",
			Method:          "callDifferentActor",
			RemoteActorID:   "11",
			RemoteActorType: "actor1",
		}

		body, _ = json.Marshal(request)

		_, status, err = utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, firstActorURL), body)
		require.NoError(t, err)
		require.Equal(t, 200, status)
	})

	t.Run("Actor cross actor call (diff pod)", func(t *testing.T) {
		// Register the 2nd actor on a different pod.
		request := actorCallRequest{
			ActorType: "actor2",
			ActorID:   "21",
			Method:    "logCall",
		}

		body, _ := json.Marshal(request)

		_, status, err := utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, secondActorURL), body)
		require.NoError(t, err)
		require.Equal(t, 200, status)

		request = actorCallRequest{
			ActorType:       "actor1",
			ActorID:         "10",
			Method:          "callDifferentActor",
			RemoteActorID:   "21",
			RemoteActorType: "actor2",
		}

		body, _ = json.Marshal(request)

		_, status, err = utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, firstActorURL), body)
		require.NoError(t, err)
		require.Equal(t, 200, status)
	})
}

func TestActorNegativeInvocation(t *testing.T) {
	firstActorURL := getExternalURL(t, "actor1")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	healthCheckApp(t, firstActorURL, numHealthChecks)

	t.Run("Try actor call with non-bound method", func(t *testing.T) {
		request := actorCallRequest{
			ActorType: "actor1",
			ActorID:   "10",
			Method:    "notAMethod",
		}

		body, _ := json.Marshal(request)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, firstActorURL), body)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 500, status)
	})

	t.Run("Try actor call with non-registered actor type", func(t *testing.T) {
		request := actorCallRequest{
			ActorType: "notAType",
			ActorID:   "10",
			Method:    "logCall",
		}

		body, _ := json.Marshal(request)

		_, err := utils.HTTPPost(fmt.Sprintf(callActorURL, firstActorURL), body)
		require.NoError(t, err)
	})
}
