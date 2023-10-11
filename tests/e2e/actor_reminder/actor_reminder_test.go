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
	"sync"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/stretchr/testify/require"
)

const (
	appName                      = "actorreminder"                      // App name in Dapr.
	actorIDRestartTemplate       = "actor-reminder-restart-test-%d"     // Template for Actor ID
	actorIDPartitionTemplate     = "actor-reminder-partition-test-%d"   // Template for Actor ID
	reminderName                 = "RestartTestReminder"                // Reminder name
	actorIDGetTemplate           = "actor-reminder-get-test-%d"         // Template for Actor ID
	reminderNameForGet           = "GetTestReminder"                    // Reminder name for getting tests
	numIterations                = 7                                    // Number of times each test should run.
	numHealthChecks              = 60                                   // Number of get calls before starting tests.
	numActorsPerThread           = 10                                   // Number of get calls before starting tests.
	secondsToCheckReminderResult = 20                                   // How much time to wait to make sure the result is in logs.
	actorName                    = "testactorreminder"                  // Actor name
	actorInvokeURLFormat         = "%s/test/" + actorName + "/%s/%s/%s" // URL to invoke a Dapr's actor method in test app.
	actorlogsURLFormat           = "%s/test/logs"                       // URL to fetch logs from test app.
	shutdownURLFormat            = "%s/test/shutdown"                   // URL to shutdown sidecar and app.
	misconfiguredAppName         = "actor-reminder-no-state-store"      // Actor-reminder app without a state store (should fail to start)
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
	utils.SetupLogs("actor_reminder")
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
				"TEST_APP_ACTOR_TYPE": actorName,
			},
		},
		{
			AppName:             misconfiguredAppName,
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
				"TEST_APP_ACTOR_TYPE": actorName,
			},
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorMissingStateStore(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(misconfiguredAppName)
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

	t.Run("Actor service should 500 when no state store is available.", func(t *testing.T) {
		_, statusCode, err := utils.HTTPPostWithStatus(fmt.Sprintf(actorInvokeURLFormat, externalURL, "bogon-actor", "reminders", "failed-reminder"), reminderBody)
		require.NoError(t, err)
		require.True(t, statusCode == 500)
	})
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

	t.Run("Actor reminder unregister then restart should not trigger anymore.", func(t *testing.T) {
		var wg sync.WaitGroup
		for iteration := 1; iteration <= numIterations; iteration++ {
			wg.Add(1)
			go func(iteration int) {
				defer wg.Done()
				t.Logf("Running iteration %d out of %d ...", iteration, numIterations)

				for i := 0; i < numActorsPerThread; i++ {
					actorID := fmt.Sprintf(actorIDRestartTemplate, i+(1000*iteration))
					t.Logf("Registering reminder: %s %s ...", actorID, reminderName)

					// Deleting pre-existing reminder, just in case…
					_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
					require.NoError(t, err)

					// Registering reminder
					_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reminderBody)
					require.NoError(t, err)
				}

				t.Logf("Sleeping for %d seconds ...", secondsToCheckReminderResult)
				time.Sleep(secondsToCheckReminderResult * time.Second)

				for i := 0; i < numActorsPerThread; i++ {
					_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
					require.NoError(t, err)

					actorID := fmt.Sprintf(actorIDRestartTemplate, i+(1000*iteration))
					// Unregistering reminder
					t.Logf("Unregistering reminder: %s %s ...", actorID, reminderName)
					_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
					require.NoError(t, err)
				}

				t.Logf("Getting logs from %s to see if reminders did trigger ...", logsURL)
				resp, httpErr := utils.HTTPGet(logsURL)
				require.NoError(t, httpErr)

				t.Log("Checking if all reminders did trigger ...")
				// Errors below should NOT be considered flakyness and must be investigated.
				// If there was no other error until now, there should be reminders triggered.
				for i := 0; i < numActorsPerThread; i++ {
					actorID := fmt.Sprintf(actorIDRestartTemplate, i+(1000*iteration))
					count := countActorAction(resp, actorID, reminderName)
					// Due to possible load stress, we do not expect all reminders to be called at the same frequency.
					// There are other E2E tests that validate the correct frequency of reminders in a happy path.
					require.True(t, count >= 1, "Reminder %s for Actor %s was invoked %d times.", reminderName, actorID, count)
				}
			}(iteration)
		}
		wg.Wait()

		err = backoff.RetryNotify(
			func() error {
				t.Logf("Checking if unregistration of reminders took place in %s ...", appName)
				resp1, err := utils.HTTPGet(logsURL)
				if err != nil {
					return err
				}

				time.Sleep(secondsToCheckReminderResult * time.Second)

				resp2, err := utils.HTTPGet(logsURL)
				if err != nil {
					return err
				}

				t.Log("Checking if NO reminder triggered after unregister and before restart ...")
				for iteration := 1; iteration <= numIterations; iteration++ {
					// This is useful to make sure the unregister call was processed.
					for i := 0; i < numActorsPerThread; i++ {
						actorID := fmt.Sprintf(actorIDRestartTemplate, i+(1000*iteration))
						count1 := countActorAction(resp1, actorID, reminderName)
						count2 := countActorAction(resp2, actorID, reminderName)
						count := count2 - count1

						if count > 0 {
							return fmt.Errorf("after unregistration but before restart, reminder %s for Actor %s was invoked %d times.", reminderName, actorID, count)
						}
					}
				}

				return nil
			},
			backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 10),
			func(err error, d time.Duration) {
				t.Logf("Error while validating reminder unregistration logs: '%v' - retrying in %s", err, d)
			},
		)
		require.NoError(t, err)

		t.Logf("Restarting %s ...", appName)
		// Shutdown the sidecar
		_, err = utils.HTTPPost(fmt.Sprintf(shutdownURLFormat, externalURL), []byte(""))
		require.NoError(t, err)

		t.Logf("Sleeping for %d seconds to see if reminders will trigger ...", secondsToCheckReminderResult)
		time.Sleep(secondsToCheckReminderResult * time.Second)

		// This initial probe makes the test wait a little bit longer when needed,
		// making this test less flaky due to delays in the deployment.
		t.Logf("Checking if app is healthy ...")
		_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
		require.NoError(t, err)

		t.Logf("Getting logs from %s ...", logsURL)
		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)

		t.Log("Checking if NO reminder triggered ...")
		for iteration := 1; iteration <= numIterations; iteration++ {
			// Errors below should NOT be considered flakyness and must be investigated.
			// After the app unregisters a reminder and is restarted, there should be no more reminders triggered.
			for i := 0; i < numActorsPerThread; i++ {
				actorID := fmt.Sprintf(actorIDRestartTemplate, i+(1000*iteration))
				count := countActorAction(resp, actorID, reminderName)
				require.True(t, count == 0, "After restart, reminder %s for Actor %s was invoked %d times.", reminderName, actorID, count)
			}
		}

		_, err = utils.HTTPDelete(logsURL)
		require.NoError(t, err)

		t.Log("Done.")
	})

	t.Run("Actor reminder register and get should succeed.", func(t *testing.T) {
		var wg sync.WaitGroup
		for iteration := 1; iteration <= numIterations; iteration++ {
			wg.Add(1)
			go func(iteration int) {
				defer wg.Done()
				t.Logf("Running iteration %d out of %d ...", iteration, numIterations)

				for i := 0; i < numActorsPerThread; i++ {
					actorID := fmt.Sprintf(actorIDGetTemplate, i+(1000*iteration))
					// Deleting pre-existing reminder
					_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderNameForGet))
					require.NoError(t, err)

					// Registering reminder
					_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderNameForGet), reminderBody)
					require.NoError(t, err)
				}

				t.Logf("Sleeping for %d seconds ...", secondsToCheckReminderResult)
				time.Sleep(secondsToCheckReminderResult * time.Second)
			}(iteration)
		}
		wg.Wait()

		t.Log("Checking reminders get succeed ...")
		for iteration := 1; iteration <= numIterations; iteration++ {
			for i := 0; i < numActorsPerThread; i++ {
				actorID := fmt.Sprintf(actorIDGetTemplate, i+(1000*iteration))

				resp, err := utils.HTTPGet(
					fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderNameForGet))
				require.NoError(t, err)
				require.True(t, len(resp) != 0, "Reminder %s does not exist", reminderNameForGet)
			}
		}

		_, err = utils.HTTPDelete(logsURL)
		require.NoError(t, err)

		t.Log("Done.")
	})
}

func TestActorReminderPeriod(t *testing.T) {
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
		Period:  "R5/PT1S",
	}
	reminderBody, err := json.Marshal(reminder)
	require.NoError(t, err)

	t.Run("Actor reminder with repetition should run correct number of times", func(t *testing.T) {
		reminderName := "repeatable-reminder"
		actorID := "repetable-reminder-actor"
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)
		// Registering reminder
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reminderBody)
		require.NoError(t, err)

		t.Logf("Sleeping for %d seconds ...", secondsToCheckReminderResult)
		time.Sleep(secondsToCheckReminderResult * time.Second)

		t.Logf("Getting logs from %s to see if reminders did trigger ...", logsURL)
		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)

		t.Log("Checking if all reminders did trigger ...")
		count := countActorAction(resp, actorID, reminderName)
		require.Equal(t, 5, count, "response: %s", string(resp))
	})
}

func TestActorReminderTTL(t *testing.T) {
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
		DueTime: "10s",
		Period:  "PT5S",
		TTL:     "59s",
	}
	reminderBody, err := json.Marshal(reminder)
	require.NoError(t, err)

	t.Run("Actor reminder with TTL should run correct number of times", func(t *testing.T) {
		reminderName := "ttl-reminder"
		actorID := "ttl-reminder-actor"
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)
		// Registering reminder
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reminderBody)
		require.NoError(t, err)

		waitForReminderWithTTLToFinishInSeconds := 60
		t.Logf("Sleeping for %d seconds ...", waitForReminderWithTTLToFinishInSeconds)
		time.Sleep(time.Duration(waitForReminderWithTTLToFinishInSeconds) * time.Second)

		t.Logf("Getting logs from %s to see if reminders did trigger ...", logsURL)
		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)

		t.Log("Checking if all reminders did trigger ...")
		count := countActorAction(resp, actorID, reminderName)
		require.InDelta(t, 10, count, 2)
	})
}

func TestActorReminderNonHostedActor(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	t.Run("Operations on actor reminders should fail if actor type is not hosted", func(t *testing.T) {
		// Run the tests
		res, err := utils.HTTPPost(externalURL+"/test/nonhosted", nil)
		require.NoError(t, err)
		require.Equal(t, "OK", string(res))
	})
}
