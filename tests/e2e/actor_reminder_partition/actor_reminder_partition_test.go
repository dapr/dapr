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

package actor_reminder_e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"go.uber.org/ratelimit"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const (
	appName                      = "actorreminderpartition"           // App name in Dapr.
	actorIDPartitionTemplate     = "actor-reminder-partition-test-%d" // Template for Actor ID.
	reminderName                 = "PartitionTestReminder"            // Reminder name.
	numIterations                = 7                                  // Number of times each test should run.
	numHealthChecks              = 60                                 // Number of get calls before starting tests.
	numActors                    = 40                                 // Number of actors to register a reminder.
	secondsToCheckReminderResult = 90                                 // How much time to wait to make sure the result is in logs.
	secondsToWaitForAppRestart   = 10                                 // How much time to wait until app has restarted.
	reminderUpdateRateLimitRPS   = 20                                 // Sane rate limiting in persisting reminders.
	actorName                    = "testactorreminderpartition"       // Actor type.
	actorInvokeURLFormat         = "%s/test/%s/%s/%s/%s"              // URL to invoke a Dapr's actor method in test app.
	actorlogsURLFormat           = "%s/test/logs"                     // URL to fetch logs from test app.
	envURLFormat                 = "%s/test/env/%s"                   // URL to fetch or set env var from test app.
	shutdownSidecarURLFormat     = "%s/test/shutdownsidecar"          // URL to shutdown sidecar.
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
				"TEST_APP_ACTOR_REMINDERS_PARTITIONS": "0",
				"TEST_APP_ACTOR_TYPE":                 actorName,
			},
			Config: "actortypemetadata",
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func validateReminderLogs(t *testing.T, numActorsToCheck int) error {
	externalURL := ""
	logsURL := ""

	rerr := backoff.Retry(func() error {
		externalURL = tr.Platform.AcquireAppExternalURL(appName)
		if externalURL == "" {
			return fmt.Errorf("external URL must not be empty!")
		}

		logsURL = fmt.Sprintf(actorlogsURLFormat, externalURL)

		t.Logf("Deleting logs via %s ...", logsURL)
		_, err := utils.HTTPDelete(logsURL)
		if err != nil {
			return err
		}

		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 10))
	if rerr != nil {
		return rerr
	}

	return backoff.Retry(func() error {
		t.Logf("Getting logs from %s to see if reminders did trigger for %d actors ...", logsURL, numActorsToCheck)
		resp, errb := utils.HTTPGet(logsURL)
		if errb != nil {
			return errb
		}

		t.Log("Checking if all reminders did trigger ...")
		// Errors below should NOT be considered flakyness and must be investigated.
		// If there was no other error until now, there should be reminders triggered.
		for i := 0; i < numActorsToCheck; i++ {
			actorID := fmt.Sprintf(actorIDPartitionTemplate, i+1000)
			count := countActorAction(resp, actorID, reminderName)
			// Due to possible load stress, we do not expect all reminders to be called at the same frequency.
			// There are other E2E tests that validate the correct frequency of reminders in a happy path.
			if count == 0 {
				t.Logf("Reminder %s for Actor %s was not invoked.", reminderName, actorID)
				return fmt.Errorf("Reminder %s for Actor %s was not invoked.", reminderName, actorID)
			}
		}

		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 10))
}

func TestActorReminder(t *testing.T) {
	rateLimit := ratelimit.New(reminderUpdateRateLimitRPS)
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

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
			rateLimit.Take()
			actorID := fmt.Sprintf(actorIDPartitionTemplate, i+1000)
			// Deleting pre-existing reminder
			_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorName, actorID, "reminders", reminderName))
			require.NoError(t, err)
		}

		expectedEnvPartitionCount := "0"
		mustCheckLogs := true
		for i := 0; i < numActors; i++ {
			//externalURL = tr.Platform.AcquireAppExternalURL(appName)
			//require.NotEmpty(t, externalURL, "external URL must not be empty!")

			rateLimit.Take()
			actorID := fmt.Sprintf(actorIDPartitionTemplate, i+1000)

			newPartitionCount := 0
			if i == numActors/4 {
				newPartitionCount = 5
				mustCheckLogs = true
			}

			if i == numActors/2 {
				newPartitionCount = 7
				mustCheckLogs = true
			}

			if newPartitionCount > 0 {
				_, err = utils.HTTPPost(
					fmt.Sprintf(envURLFormat, externalURL, "TEST_APP_ACTOR_REMINDERS_PARTITIONS"),
					[]byte(strconv.Itoa(newPartitionCount)))
				require.NoError(t, err)

				// Shutdown the sidecar to load the new partition config
				_, err = utils.HTTPPost(fmt.Sprintf(shutdownSidecarURLFormat, externalURL), []byte(""))
				err = tr.Platform.SetAppEnv(appName, "TEST_APP_ACTOR_REMINDERS_PARTITIONS", strconv.Itoa(newPartitionCount))
				require.NoError(t, err)

				t.Logf("Waiting for app %s to restart ...", appName)

				// Sleep for some time to let the sidecar restart.
				// Calling the health-check right away might trigger a false-positive health prior to actual restart.
				time.Sleep(secondsToWaitForAppRestart * 12 * time.Second)

				expectedEnvPartitionCount = strconv.Itoa(newPartitionCount)
			}

			err = backoff.Retry(func() error {
				//externalURL = tr.Platform.AcquireAppExternalURL(appName)
				//if externalURL == "" {
				//	return fmt.Errorf("external URL must not be empty!")
				//}

				_, rerr := utils.HTTPGetNTimes(externalURL, numHealthChecks)
				if rerr != nil {
					return rerr
				}

				envValue, rerr := utils.HTTPGet(fmt.Sprintf(envURLFormat, externalURL, "TEST_APP_ACTOR_REMINDERS_PARTITIONS"))
				if rerr != nil {
					return rerr
				}
				if expectedEnvPartitionCount != string(envValue) {
					return fmt.Errorf("invalid number of partitions: %s (expected) vs %s (actual)", expectedEnvPartitionCount, string(envValue))
				}

				return nil
			}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 10))
			require.NoError(t, err)

			err = backoff.Retry(func() error {
				// Registering reminder
				_, httpStatusCode, rerr := utils.HTTPPostWithStatus(
					fmt.Sprintf(actorInvokeURLFormat, externalURL, actorName, actorID, "reminders", reminderName), reminderBody)
				if rerr != nil {
					return rerr
				}

				if (httpStatusCode != 200) && (httpStatusCode != 204) {
					return fmt.Errorf("invalid status code %d while registering reminder for actorID %s", httpStatusCode, actorID)
				}
				return nil

			}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 10))
			require.NoError(t, err)

			if mustCheckLogs {
				err = validateReminderLogs(t, i+1)
				require.NoError(t, err)
				mustCheckLogs = false
			}
		}

		err = validateReminderLogs(t, numActors)
		require.NoError(t, err)

		for i := 0; i < numActors; i++ {
			rateLimit.Take()
			actorID := fmt.Sprintf(actorIDPartitionTemplate, i+1000)
			// Unregistering reminder
			_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorName, actorID, "reminders", reminderName))
			require.NoError(t, err)
		}

		t.Log("Done.")
	})
}
