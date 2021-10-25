// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actor_reminder_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const (
	appName                      = "actorreminderpartition"                      // App name in Dapr.
	actorIDPartitionTemplate     = "actor-reminder-partition-test-%d"            // Template for Actor ID.
	reminderName                 = "PartitionTestReminder"                       // Reminder name.
	numIterations                = 7                                             // Number of times each test should run.
	numHealthChecks              = 60                                            // Number of get calls before starting tests.
	numActors                    = 40                                            // Number of actors to register a reminder.
	secondsToCheckReminderResult = 90                                            // How much time to wait to make sure the result is in logs.
	actorInvokeURLFormat         = "%s/test/testactorreminderpartition/%s/%s/%s" // URL to invoke a Dapr's actor method in test app.
	actorlogsURLFormat           = "%s/test/logs"                                // URL to fetch logs from test app.
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
	Callback string `json:"callback,omitempty"`
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
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        appName,
			DaprEnabled:    true,
			ImageName:      "e2e-actorfeatures",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			DaprCPULimit:   "2.0",
			DaprCPURequest: "0.1",
			AppCPULimit:    "2.0",
			AppCPURequest:  "0.1",
			AppEnv: map[string]string{
				"TEST_APP_ACTOR_TYPE": "testactorreminderpartition",
			},
			Config: "actortypemetadata",
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorReminder(t *testing.T) {
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
		Data:    "reminderdata",
		DueTime: "1s",
		Period:  "1s",
	}
	reminderBody, err := json.Marshal(reminder)
	require.NoError(t, err)

	t.Run("Actor reminder changes number of partitions.", func(t *testing.T) {
		for i := 0; i < numActors; i++ {
			if i == numActors/4 {
				// We set the number of partitions midway.
				// This will test that the migration path also works from the old format.
				// And validate that new reminders can work after setting number of partitions.
				tr.Platform.SetAppEnv(appName, "TEST_APP_ACTOR_REMINDERS_PARTITIONS", "5")
			}

			if i == numActors/2 {
				// We increase the number of partitions midway.
				// This will test that the migration path also works with a partition change.
				// And validate that new reminders can work after increasing number of partitions.
				tr.Platform.SetAppEnv(appName, "TEST_APP_ACTOR_REMINDERS_PARTITIONS", "7")
			}

			actorID := fmt.Sprintf(actorIDPartitionTemplate, i+1000)
			// Deleting pre-existing reminder
			_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
			require.NoError(t, err)

			// Registering reminder
			_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reminderBody)
			require.NoError(t, err)
		}

		t.Logf("Sleeping for %d seconds ...", secondsToCheckReminderResult)
		time.Sleep(secondsToCheckReminderResult * time.Second)

		for i := 0; i < numActors; i++ {
			_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
			require.NoError(t, err)

			actorID := fmt.Sprintf(actorIDPartitionTemplate, i+1000)
			// Unregistering reminder
			_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
			require.NoError(t, err)
		}

		t.Logf("Getting logs from %s to see if reminders did trigger ...", logsURL)
		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)

		t.Log("Checking if all reminders did trigger ...")
		// Errors below should NOT be considered flakyness and must be investigated.
		// If there was no other error until now, there should be reminders triggered.
		for i := 0; i < numActors; i++ {
			actorID := fmt.Sprintf(actorIDPartitionTemplate, i+1000)
			count := countActorAction(resp, actorID, reminderName)
			// Due to possible load stress, we do not expect all reminders to be called at the same frequency.
			// There are other E2E tests that validate the correct frequency of reminders in a happy path.
			require.True(t, count >= 1, "Reminder %s for Actor %s was invoked %d times.", reminderName, actorID, count)
		}

		t.Log("Done.")
	})
}
