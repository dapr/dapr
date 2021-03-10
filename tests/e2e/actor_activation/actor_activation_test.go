// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actor_activation_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	appName                          = "actorapp"                                // App name in Dapr.
	numHealthChecks                  = 60                                        // Number of get calls before starting tests.
	secondsToCheckActorRemainsActive = 1                                         // How much time to wait to make sure actor is deactivated
	secondsToCheckActorDeactivation  = 20                                        // How much time to wait to make sure actor is deactivated
	actorInvokeURLFormat             = "%s/test/testactor/%s/method/actormethod" // URL to invoke a Dapr's actor method in test app.
	actorlogsURLFormat               = "%s/test/logs"                            // URL to fetch logs from test app.
)

// represents a response for the APIs in this app.
type actorLogEntry struct {
	Action    string `json:"action,omitempty"`
	ActorType string `json:"actorType,omitempty"`
	ActorID   string `json:"actorId,omitempty"`
	Timestamp int    `json:"timestamp,omitempty"`
}

func parseLogEntries(resp []byte) []actorLogEntry {
	logEntries := []actorLogEntry{}
	err := json.Unmarshal(resp, &logEntries)
	if err != nil {
		return nil
	}

	return logEntries
}

func findActorActivation(resp []byte, actorId string) bool {
	return findActorAction(resp, actorId, "activation")
}

func findActorDeactivation(resp []byte, actorId string) bool {
	return findActorAction(resp, actorId, "deactivation")
}

func findActorMethodInvokation(resp []byte, actorId string) bool {
	return findActorAction(resp, actorId, "actormethod")
}

func findActorAction(resp []byte, actorId string, action string) bool {
	logEntries := parseLogEntries(resp)
	for _, logEntry := range logEntries {
		if (logEntry.ActorID == actorId) && (logEntry.Action == action) {
			return true
		}
	}

	return false
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-actorapp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorActivation(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	logsURL := fmt.Sprintf(actorlogsURLFormat, externalURL)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Wait until runtime finds the leader of placements.
	time.Sleep(15 * time.Second)

	t.Run("Actor deactivates due to timeout.", func(t *testing.T) {
		actorId := "100"

		invokeURL := fmt.Sprintf(actorInvokeURLFormat, externalURL, actorId)

		_, err = utils.HTTPPost(invokeURL, []byte{})
		require.NoError(t, err)

		fmt.Printf("getting logs, the current time is %s\n", time.Now())
		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)

		// there is no longer an activate message
		require.False(t, findActorActivation(resp, actorId))

		require.True(t, findActorMethodInvokation(resp, actorId))
		require.False(t, findActorDeactivation(resp, actorId))

		time.Sleep(secondsToCheckActorDeactivation * time.Second)

		fmt.Printf("getting logs, the current time is %s\n", time.Now())
		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)

		// there is no longer an activate message
		require.False(t, findActorActivation(resp, actorId))

		require.True(t, findActorMethodInvokation(resp, actorId))
		require.True(t, findActorDeactivation(resp, actorId))
	})

	t.Run("Actor does not deactivate since there is no timeout.", func(t *testing.T) {
		actorId := guuid.New().String()
		invokeURL := fmt.Sprintf(actorInvokeURLFormat, externalURL, actorId)

		_, err = utils.HTTPPost(invokeURL, []byte{})
		require.NoError(t, err)

		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)

		// there is no longer an activate message
		require.False(t, findActorActivation(resp, actorId))

		require.True(t, findActorMethodInvokation(resp, actorId))
		require.False(t, findActorDeactivation(resp, actorId))

		time.Sleep(secondsToCheckActorRemainsActive * time.Second)

		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)

		// there is no longer an activate message
		require.False(t, findActorActivation(resp, actorId))

		require.True(t, findActorMethodInvokation(resp, actorId))
		require.False(t, findActorDeactivation(resp, actorId))
	})
}
