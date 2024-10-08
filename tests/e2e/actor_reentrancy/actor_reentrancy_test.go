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

package reentrancy

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	appName              = "reentrantactor"                         // App name in Dapr.
	actorInvokeURLFormat = "%s/test/actors/reentrantActor/%s/%s/%s" // URL to invoke a Dapr's actor method in test app.
	actorlogsURLFormat   = "%s/test/logs"                           // URL to fetch logs from test app.
)

// represents a response for the APIs in this app.
type actorLogEntry struct {
	Action         string `json:"action,omitempty"`
	ActorType      string `json:"actorType,omitempty"`
	ActorID        string `json:"actorId,omitempty"`
	StartTimestamp int    `json:"startTimestamp,omitempty"`
	EndTimestamp   int    `json:"endTimestamp,omitempty"`
}

type reentrantRequest struct {
	Calls []actorCall `json:"calls,omitempty"`
}

type actorCall struct {
	ActorID   string `json:"id"`
	ActorType string `json:"type"`
	Method    string `json:"method"`
}

func parseLogEntries(resp []byte) []actorLogEntry {
	logEntries := []actorLogEntry{}
	err := json.Unmarshal(resp, &logEntries)
	if err != nil {
		return nil
	}

	return logEntries
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("actor_reentrancy")
	utils.InitHTTPClient(true)

	// This test can be run outside of Kubernetes too
	// Run the actorreentrancy e2e app using, for example, the Dapr CLI:
	//   PORT=22222 dapr run --app-id reentrantactor --resources-path ./resources --app-port 22222 -- go run .
	// Then run this test with the env var "APP_ENDPOINT" pointing to the address of the app. For example:
	//   APP_ENDPOINT="http://localhost:22222" DAPR_E2E_TEST="actor_reentrancy" make test-clean test-e2e-all |& tee test.log
	if os.Getenv("APP_ENDPOINT") == "" {
		testApps := []kube.AppDescription{
			{
				AppName:             appName,
				DaprEnabled:         true,
				DebugLoggingEnabled: true,
				ImageName:           "e2e-actorreentrancy",
				Config:              "omithealthchecksconfig",
				Replicas:            1,
				IngressEnabled:      true,
				MetricsEnabled:      true,
				DaprCPULimit:        "2.0",
				DaprCPURequest:      "0.1",
				AppCPULimit:         "2.0",
				AppCPURequest:       "0.1",
				AppPort:             22222,
			},
		}

		tr = runner.NewTestRunner(appName, testApps, nil, nil)
		os.Exit(tr.Start(m))
	} else {
		os.Exit(m.Run())
	}
}

func getAppEndpoint(t *testing.T) string {
	if env := os.Getenv("APP_ENDPOINT"); env != "" {
		return env
	}

	u := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, u, "external URL for for app must not be empty")
	return u
}

func TestActorReentrancy(t *testing.T) {
	reentrantURL := getAppEndpoint(t)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	require.NoError(t, utils.HealthCheckApps(reentrantURL))

	const (
		firstActorID  = "1"
		secondActorID = "2"
		actorType     = "reentrantActor"
	)

	logsURL := fmt.Sprintf(actorlogsURLFormat, reentrantURL)

	// This basic test makes it possible to assert that the actor subsystem is ready
	t.Run("Readiness", func(t *testing.T) {
		body, _ := json.Marshal(actorCall{
			ActorType: "actor1",
			ActorID:   "hi",
			Method:    "helloMethod",
		})

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			_, status, err := utils.HTTPPostWithStatus(fmt.Sprintf(actorInvokeURLFormat, reentrantURL, "hi", "method", "helloMethod"), body)
			require.NoError(t, err)
			assert.Equal(t, 200, status)
		}, 15*time.Second, 200*time.Millisecond)
	})

	t.Run("Same Actor Reentrancy", func(t *testing.T) {
		utils.HTTPDelete(logsURL)
		req := reentrantRequest{
			Calls: []actorCall{
				{ActorID: firstActorID, ActorType: actorType, Method: "reentrantMethod"},
				{ActorID: firstActorID, ActorType: actorType, Method: "standardMethod"},
			},
		}

		reqBody, _ := json.Marshal(req)
		_, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, reentrantURL, firstActorID, "method", "reentrantMethod"), reqBody)
		require.NoError(t, err)

		resp, httpErr := utils.HTTPGet(logsURL)
		require.NoError(t, httpErr)
		require.NotNil(t, resp)

		logs := parseLogEntries(resp)

		require.Lenf(t, logs, len(req.Calls)*2, "Logs: %v", logs)
		index := 0
		for i := 0; i < len(req.Calls); i++ {
			require.Equal(t, fmt.Sprintf("Enter %s", req.Calls[i].Method), logs[index].Action)
			require.Equal(t, req.Calls[i].ActorID, logs[index].ActorID)
			require.Equal(t, req.Calls[i].ActorType, logs[index].ActorType)
			index++
		}

		for i := len(req.Calls) - 1; i > -1; i-- {
			require.Equal(t, fmt.Sprintf("Exit %s", req.Calls[i].Method), logs[index].Action)
			require.Equal(t, req.Calls[i].ActorID, logs[index].ActorID)
			require.Equal(t, req.Calls[i].ActorType, logs[index].ActorType)
			index++
		}
	})

	t.Run("Multi-actor Reentrancy", func(t *testing.T) {
		utils.HTTPDelete(logsURL)
		req := reentrantRequest{
			Calls: []actorCall{
				{ActorID: firstActorID, ActorType: actorType, Method: "reentrantMethod"},
				{ActorID: secondActorID, ActorType: actorType, Method: "reentrantMethod"},
				{ActorID: firstActorID, ActorType: actorType, Method: "standardMethod"},
			},
		}

		reqBody, _ := json.Marshal(req)
		_, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, reentrantURL, firstActorID, "method", "reentrantMethod"), reqBody)
		require.NoError(t, err)

		resp, httpErr := utils.HTTPGet(logsURL)
		require.NoError(t, httpErr)
		require.NotNil(t, resp)

		logs := parseLogEntries(resp)

		require.Lenf(t, logs, len(req.Calls)*2, "Logs: %v", logs)

		index := 0
		for i := 0; i < len(req.Calls); i++ {
			require.Equal(t, fmt.Sprintf("Enter %s", req.Calls[i].Method), logs[index].Action)
			require.Equal(t, req.Calls[i].ActorID, logs[index].ActorID)
			require.Equal(t, req.Calls[i].ActorType, logs[index].ActorType)
			index++
		}

		for i := len(req.Calls) - 1; i > -1; i-- {
			require.Equal(t, fmt.Sprintf("Exit %s", req.Calls[i].Method), logs[index].Action)
			require.Equal(t, req.Calls[i].ActorID, logs[index].ActorID)
			require.Equal(t, req.Calls[i].ActorType, logs[index].ActorType)
			index++
		}
	})

	t.Run("Reentrancy Stack Depth Limit", func(t *testing.T) {
		utils.HTTPDelete(logsURL)
		// Limit is set to 5
		req := reentrantRequest{
			Calls: []actorCall{
				{ActorID: firstActorID, ActorType: actorType, Method: "reentrantMethod"},
				{ActorID: firstActorID, ActorType: actorType, Method: "reentrantMethod"},
				{ActorID: firstActorID, ActorType: actorType, Method: "reentrantMethod"},
				{ActorID: firstActorID, ActorType: actorType, Method: "reentrantMethod"},
				{ActorID: firstActorID, ActorType: actorType, Method: "reentrantMethod"},
				{ActorID: firstActorID, ActorType: actorType, Method: "standardMethod"},
			},
		}

		reqBody, _ := json.Marshal(req)
		_, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, reentrantURL, firstActorID, "method", "reentrantMethod"), reqBody)
		require.NoError(t, err)

		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)
		require.NotNil(t, resp)

		logs := parseLogEntries(resp)

		// Should be 2 calls per stack item (limit 5)
		require.Len(t, logs, 10)
		for index := range logs {
			if index < 5 {
				require.Equal(t, "Enter reentrantMethod", logs[index].Action)
			} else {
				require.Equal(t, "Error reentrantMethod", logs[index].Action)
			}
		}
	})
}
