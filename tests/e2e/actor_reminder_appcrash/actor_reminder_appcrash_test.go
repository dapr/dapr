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

package actor_reminder_appcrash_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	appName                      = "actorreminderappcrash"              // App name in Dapr.
	actorIDAppCrashTemplate      = "actor-reminder-appcrash-test-%s"    // Template for Actor ID
	reminderName                 = "AppCrashTestReminder"               // Reminder name
	numIterations                = 7                                    // Number of times each test should run.
	numHealthChecks              = 60                                   // Number of get calls before starting tests.
	secondsToCheckReminderResult = 20                                   // How much time to wait to make sure the result is in logs.
	actorName                    = "testactorreminderappcrash"          // Actor name
	actorInvokeURLFormat         = "%s/test/" + actorName + "/%s/%s/%s" // URL to invoke a Dapr's actor method in test app.
	actorlogsURLFormat           = "%s/test/logs"                       // URL to fetch logs from test app.
	shutdownAppURLFormat         = "%s/test/shutdownapp"                // URL to shutdown sidecar and app.
)

// represents a response for the APIs in this app.
type actorLogEntry struct {
	Action         string `json:"action,omitempty"`
	ActorType      string `json:"actorType,omitempty"`
	ActorID        string `json:"actorId,omitempty"`
	StartTimestamp int    `json:"startTimestamp,omitempty"`
	EndTimestamp   int    `json:"endTimestamp,omitempty"`
}

type actorReminder struct {
	Data     string `json:"data,omitempty"`
	DueTime  string `json:"dueTime,omitempty"`
	Period   string `json:"period,omitempty"`
	TTL      string `json:"ttl,omitempty"`
	Callback string `json:"callback,omitempty"`
}

type reminderResponse struct {
	ActorID        string      `json:"actorID,omitempty"`
	ActorType      string      `json:"actorType,omitempty"`
	Name           string      `json:"name,omitempty"`
	Data           interface{} `json:"data"`
	Period         string      `json:"period"`
	DueTime        string      `json:"dueTime"`
	RegisteredTime string      `json:"registeredTime,omitempty"`
	ExpirationTime string      `json:"expirationTime,omitempty"`
}

func parseLogEntries(resp []byte) []actorLogEntry {
	logEntries := []actorLogEntry{}
	err := json.Unmarshal(resp, &logEntries)
	if err != nil {
		return nil
	}

	return logEntries
}

func countActorAction(resp []byte, actorID string, action string) int {
	count := 0
	logEntries := parseLogEntries(resp)
	for _, logEntry := range logEntries {
		if (logEntry.ActorID == actorID) && (logEntry.Action == action) {
			count++
		}
	}

	return count
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("actor_reminder_appcrash")
	utils.InitHTTPClient(false)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:             appName,
			DaprEnabled:         true,
			DebugLoggingEnabled: true,
			ImageName:           "e2e-actorfeatures",
			Config:              "omithealthchecksconfig",
			Replicas:            1,
			IngressEnabled:      true,
			DaprCPULimit:        "2.0",
			DaprCPURequest:      "0.1",
			AppCPULimit:         "2.0",
			AppCPURequest:       "0.1",
			AppEnv: map[string]string{
				"TEST_APP_ACTOR_TYPE":  actorName,
				"TEST_APP_START_DELAY": "10s",
			},
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorReminderWithAppCrash(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	logsURL := fmt.Sprintf(actorlogsURLFormat, externalURL)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	t.Logf("Checking if app is healthy ...")
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	// Set reminder
	reminder := actorReminder{
		Data:    "reminderdataonappcrash",
		DueTime: "10s",
	}
	reminderBody, err := json.Marshal(reminder)
	require.NoError(t, err)

	t.Run("Actor reminder register then app crash should trigger eventually.", func(t *testing.T) {
		actorID := fmt.Sprintf(actorIDAppCrashTemplate, uuid.New().String())
		t.Logf("Registering reminder: %s %s ...", actorID, reminderName)

		// Registering reminder
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reminderBody)
		require.NoError(t, err)

		// This test must be timed:
		// t0 -> app started and is healthy (after any late app start)
		// t1 -> reminder is registered
		// t2 = t1 + 8s -> app is terminated
		// t3 = t1 + 10s -> reminder is due but app is down.
		// t4 = t2 + 10s -> app is healthy again
		// t5 = t4 + 20s -> validate that reminder was fired.
		time.Sleep(8 * time.Second)

		t.Logf("Restarting app %s ...", appName)
		// Shutdown the app
		_, err = utils.HTTPPost(fmt.Sprintf(shutdownAppURLFormat, externalURL), []byte(""))
		require.NoError(t, err)

		time.Sleep(secondsToCheckReminderResult * time.Second)

		err = backoff.RetryNotify(
			func() error {
				t.Logf("Checking if reminder fired in %s ...", appName)
				resp, err := utils.HTTPGet(logsURL)
				if err != nil {
					return err
				}

				reminderTriggerCount := countActorAction(resp, actorID, reminderName)

				if reminderTriggerCount == 0 {
					return fmt.Errorf("reminder %s for Actor %s was never invoked.", reminderName, actorID)
				}

				return nil
			},
			backoff.WithMaxRetries(backoff.NewConstantBackOff(1*time.Second), 10),
			func(err error, d time.Duration) {
				t.Logf("Error while validating reminder trigger logs: '%v' - retrying in %s", err, d)
			},
		)
		require.NoError(t, err)

		t.Log("Done.")
	})

}
