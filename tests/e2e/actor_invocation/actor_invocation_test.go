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

package actor_invocation_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	appName      = "actorinvocationapp"      // App name in Dapr.
	callActorURL = "%s/test/callActorMethod" // URL to force Actor registration
)

type actorCallRequest struct {
	ActorType       string `json:"actorType"`
	ActorID         string `json:"actorId"`
	Method          string `json:"method"`
	RemoteActorID   string `json:"remoteId,omitempty"`
	RemoteActorType string `json:"remoteType,omitempty"`
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("actor_invocation")
	utils.InitHTTPClient(true)

	// This test can be run outside of Kubernetes too
	// Run the actorinvocationapp e2e apps using, for example, the Dapr CLI:
	//   PORT=3001 dapr run --app-id actor1 --resources-path ./resources --app-port 3001 -- go run .
	//   PORT=3002 dapr run --app-id actor2 --resources-path ./resources --app-port 3002 -- go run .
	// Then run this test with the env var "APP1_ENDPOINT" and "APP2_ENDPOINT" pointing to the addresses of the apps. For example:
	//   APP1_ENDPOINT="http://localhost:3001" APP2_ENDPOINT="http://localhost:3002" DAPR_E2E_TEST="actor_invocation" make test-clean test-e2e-all |& tee test.log
	if os.Getenv("APP1_ENDPOINT") == "" && os.Getenv("APP2_ENDPOINT") == "" {
		testApps := []kube.AppDescription{
			{
				AppName:             "actor1",
				DaprEnabled:         true,
				ImageName:           "e2e-actorinvocationapp",
				DebugLoggingEnabled: true,
				Replicas:            1,
				IngressEnabled:      true,
				MetricsEnabled:      true,
				AppEnv: map[string]string{
					"TEST_APP_ACTOR_TYPES": "actor1,actor2,resiliencyInvokeActor",
				},
			},
			{
				AppName:             "actor2",
				DaprEnabled:         true,
				ImageName:           "e2e-actorinvocationapp",
				DebugLoggingEnabled: true,
				Replicas:            1,
				IngressEnabled:      true,
				MetricsEnabled:      true,
				AppEnv: map[string]string{
					"TEST_APP_ACTOR_TYPES": "actor1,actor2",
				},
			},
		}

		tr = runner.NewTestRunner(appName, testApps, nil, nil)
		os.Exit(tr.Start(m))
	} else {
		os.Exit(m.Run())
	}
}

func getAppEndpoint(t *testing.T, num int) string {
	s := strconv.Itoa(num)
	if env := os.Getenv("APP" + s + "_ENDPOINT"); env != "" {
		return env
	}

	u := tr.Platform.AcquireAppExternalURL("actor" + s)
	require.NotEmptyf(t, u, "external URL for actor%d must not be empty", num)
	return u
}

func TestActorInvocation(t *testing.T) {
	firstActorURL := getAppEndpoint(t, 1)
	secondActorURL := getAppEndpoint(t, 2)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	require.NoError(t, utils.HealthCheckApps(firstActorURL, secondActorURL))

	// This basic test is already covered in actor_feature_tests but serves as a sanity check here to ensure apps are up and the actor subsystem is ready.
	t.Run("Actor remote invocation", func(t *testing.T) {
		body, _ := json.Marshal(actorCallRequest{
			ActorType: "actor1",
			ActorID:   "10",
			Method:    "logCall",
		})

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			_, status, err := utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, firstActorURL), body)
			require.NoError(t, err)
			assert.Equal(t, 200, status)
		}, 15*time.Second, 200*time.Millisecond)

		body, _ = json.Marshal(actorCallRequest{
			ActorType: "actor2",
			ActorID:   "20",
			Method:    "logCall",
		})

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			_, status, err := utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, secondActorURL), body)
			require.NoError(t, err)
			assert.Equal(t, 200, status)
		}, 15*time.Second, 200*time.Millisecond)
	})

	// Validates special error handling for actors is working in runtime (.NET SDK case).
	t.Run("Actor local invocation with X-DaprErrorResponseHeader + Resiliency", func(t *testing.T) {
		request := actorCallRequest{
			ActorType: "resiliencyInvokeActor",
			ActorID:   "981",
			Method:    "xDaprErrorResponseHeader",
		}

		body, _ := json.Marshal(request)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, firstActorURL), body)
		require.NoError(t, err)
		assert.Equal(t, 200, status)
		assert.Equal(t,
			"x-DaprErrorResponseHeader call with - actorType: resiliencyInvokeActor, actorId: 981",
			string(resp))
	})

	// Validates special error handling for actors is working in runtime (.NET SDK case).
	t.Run("Actor remote invocation with X-DaprErrorResponseHeader + Resiliency", func(t *testing.T) {
		request := actorCallRequest{
			ActorType: "resiliencyInvokeActor",
			ActorID:   "789",
			Method:    "xDaprErrorResponseHeader",
		}

		body, _ := json.Marshal(request)

		resp, status, err := utils.HTTPPostWithStatus(fmt.Sprintf(callActorURL, secondActorURL), body)
		require.NoError(t, err)
		assert.Equal(t, 200, status)
		assert.Equal(t,
			"x-DaprErrorResponseHeader call with - actorType: resiliencyInvokeActor, actorId: 789",
			string(resp))
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
	firstActorURL := getAppEndpoint(t, 1)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	require.NoError(t, utils.HealthCheckApps(firstActorURL))

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
