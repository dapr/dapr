// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package reentrancy

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
	appName              = "reentrantactor"                         // App name in Dapr.
	numHealthChecks      = 60                                       // Number of get calls before starting tests.
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
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-actorreentrancy",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			DaprCPULimit:   "2.0",
			DaprCPURequest: "0.1",
			AppCPULimit:    "2.0",
			AppCPURequest:  "0.1",
			Config:         "reentrantconfig",
			AppPort:        22222,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorReentrancy(t *testing.T) {
	reentrantURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, reentrantURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(reentrantURL, numHealthChecks)
	require.NoError(t, err)

	firstActorID := "1"
	secondActorID := "2"
	actorType := "reentrantActor"

	logsURL := fmt.Sprintf(actorlogsURLFormat, reentrantURL)

	t.Run("Same Actor Reentrancy", func(t *testing.T) {
		utils.HTTPDelete(logsURL)
		req := reentrantRequest{
			Calls: []actorCall{
				{ActorID: firstActorID, ActorType: actorType, Method: "reentrantMethod"},
				{ActorID: firstActorID, ActorType: actorType, Method: "standardMethod"},
			},
		}

		reqBody, _ := json.Marshal(req)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, reentrantURL, firstActorID, "method", "reentrantMethod"), reqBody)
		require.NoError(t, err)

		resp, httpErr := utils.HTTPGet(logsURL)
		require.NoError(t, httpErr)
		require.NotNil(t, resp)

		logs := parseLogEntries(resp)

		require.Len(t, logs, len(req.Calls)*2)
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
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, reentrantURL, firstActorID, "method", "reentrantMethod"), reqBody)
		require.NoError(t, err)

		resp, httpErr := utils.HTTPGet(logsURL)
		require.NoError(t, httpErr)
		require.NotNil(t, resp)

		logs := parseLogEntries(resp)

		require.Len(t, logs, len(req.Calls)*2)

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
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, reentrantURL, firstActorID, "method", "reentrantMethod"), reqBody)
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
