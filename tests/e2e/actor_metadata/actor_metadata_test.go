//go:build e2e
// +build e2e

/*
Copyright 2023 The Dapr Authors
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

package actor_metadata_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	testRunnerName               = "actormetadata"           // Name of the test runner.
	appNameOne                   = "actormetadata-a"         // App name in Dapr.
	appNameTwo                   = "actormetadata-b"         // App name in Dapr.
	reminderName                 = "myreminder"              // Reminder name
	numHealthChecks              = 60                        // Number of get calls before starting tests.
	numActors                    = 30                        // Number of get calls before starting tests.
	secondsToCheckReminderResult = 45                        // How much time to wait to make sure the result is in logs.
	maxNumPartitions             = 4                         // Maximum number of partitions.
	actorlogsURLFormat           = "%s/test/logs"            // URL to fetch logs from test app.
	envURLFormat                 = "%s/test/env/%s"          // URL to fetch or set env var from test app.
	shutdownSidecarURLFormat     = "%s/test/shutdownsidecar" // URL to shutdown sidecar only.
)

var (
	actorName            = "testactormetadata-" + uuid.NewString() // Actor name
	actorInvokeURLFormat = "%s/test/" + actorName + "/%s/%s/%s"    // URL to invoke a Dapr's actor method in test app.
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
	ActorID        string `json:"actorID,omitempty"`
	ActorType      string `json:"actorType,omitempty"`
	Name           string `json:"name,omitempty"`
	Data           any    `json:"data"`
	Period         string `json:"period"`
	DueTime        string `json:"dueTime"`
	RegisteredTime string `json:"registeredTime,omitempty"`
	ExpirationTime string `json:"expirationTime,omitempty"`
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
	utils.SetupLogs("actor_metadata")
	utils.InitHTTPClient(false)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:             appNameOne,
			DaprEnabled:         true,
			ImageName:           "e2e-actorfeatures",
			Replicas:            1,
			IngressEnabled:      true,
			Config:              "omithealthchecksconfig",
			DebugLoggingEnabled: true,
			DaprCPULimit:        "2.0",
			DaprCPURequest:      "0.1",
			AppCPULimit:         "2.0",
			AppCPURequest:       "0.1",
			AppEnv: map[string]string{
				"TEST_APP_ACTOR_REMINDERS_PARTITIONS": "4",
				"TEST_APP_ACTOR_TYPE":                 actorName,
			},
		},
		{
			AppName:             appNameTwo,
			DaprEnabled:         true,
			ImageName:           "e2e-actorfeatures",
			Replicas:            1,
			IngressEnabled:      true,
			Config:              "omithealthchecksconfig",
			DebugLoggingEnabled: true,
			DaprCPULimit:        "2.0",
			DaprCPURequest:      "0.1",
			AppCPULimit:         "2.0",
			AppCPURequest:       "0.1",
			AppEnv: map[string]string{
				"TEST_APP_ACTOR_REMINDERS_PARTITIONS": "4",
				"TEST_APP_ACTOR_TYPE":                 actorName,
			},
		},
	}

	tr = runner.NewTestRunner(testRunnerName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorMetadataEtagRace(t *testing.T) {
	externalURLOne := tr.Platform.AcquireAppExternalURL(appNameOne)
	require.NotEmpty(t, externalURLOne, "external URL #1 must not be empty!")
	externalURLTwo := tr.Platform.AcquireAppExternalURL(appNameTwo)
	require.NotEmpty(t, externalURLTwo, "external URL #2 must not be empty!")

	logsURLOne := fmt.Sprintf(actorlogsURLFormat, externalURLOne)
	logsURLTwo := fmt.Sprintf(actorlogsURLFormat, externalURLTwo)

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	t.Log("Checking if apps are healthy")
	err := utils.HealthCheckApps(externalURLOne, externalURLTwo)
	require.NoError(t, err, "Health checks failed")

	// Set reminder
	reminder := actorReminder{
		Data:    "reminderdata",
		DueTime: "1s",
		Period:  "1s",
	}
	reminderBody, err := json.Marshal(reminder)
	require.NoError(t, err)

	t.Run("Triggers rebalance of reminders multiple times to validate eTag race on metadata record", func(t *testing.T) {
		for actorIDint := 0; actorIDint < numActors; actorIDint++ {
			actorID := strconv.Itoa(actorIDint)
			t.Logf("Registering reminder: %s %s ...", actorID, reminderName)

			// Deleting pre-existing reminder, just in case…
			_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURLOne, actorID, "reminders", reminderName))
			require.NoError(t, err)

			// Registering reminder
			_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURLOne, actorID, "reminders", reminderName), reminderBody)
			require.NoError(t, err)
		}

		for newPartitionCount := 2; newPartitionCount <= maxNumPartitions; newPartitionCount++ {
			npc := strconv.Itoa(newPartitionCount)

			// The added querystrings serve no purpose besides acting as bookmark in logs
			qs := "?partitionCount=" + npc

			_, err = utils.HTTPPost(
				fmt.Sprintf(envURLFormat, externalURLOne, "TEST_APP_ACTOR_REMINDERS_PARTITIONS")+qs,
				[]byte(npc))
			require.NoError(t, err)
			_, err = utils.HTTPPost(
				fmt.Sprintf(envURLFormat, externalURLTwo, "TEST_APP_ACTOR_REMINDERS_PARTITIONS")+qs,
				[]byte(npc))
			require.NoError(t, err)

			// Shutdown the sidecar to load the new partition config
			_, err = utils.HTTPPost(fmt.Sprintf(shutdownSidecarURLFormat, externalURLOne)+qs, nil)
			require.NoError(t, err)
			_, err = utils.HTTPPost(fmt.Sprintf(shutdownSidecarURLFormat, externalURLTwo)+qs, nil)
			require.NoError(t, err)

			// Reset logs
			_, err = utils.HTTPDelete(logsURLOne + qs)
			require.NoError(t, err)
			_, err = utils.HTTPDelete(logsURLTwo + qs)
			require.NoError(t, err)

			log.Printf("Waiting for sidecars %s & %s to restart ...", appNameOne, appNameTwo)

			t.Logf("Sleeping for %d seconds ...", secondsToCheckReminderResult)
			time.Sleep(secondsToCheckReminderResult * time.Second)

			// Define the backoff strategy
			bo := backoff.NewExponentialBackOff()
			bo.InitialInterval = 1 * time.Second
			const maxRetries = 20

			err = backoff.RetryNotify(
				func() error {
					rerr := utils.HealthCheckApps(externalURLOne, externalURLTwo)
					if rerr != nil {
						return rerr
					}

					t.Logf("Getting logs from %s to see if reminders did trigger ...", logsURLOne)
					respOne, rerr := utils.HTTPGet(logsURLOne + qs)
					if rerr != nil {
						return rerr
					}
					t.Logf("Getting logs from %s to see if reminders did trigger ...", logsURLTwo)
					respTwo, rerr := utils.HTTPGet(logsURLTwo + qs)
					if rerr != nil {
						return rerr
					}

					t.Logf("Checking if all reminders did trigger with partition count as %d ...", newPartitionCount)
					// Errors below should NOT be considered flakyness and must be investigated.
					// If there was no other error until now, there should be reminders triggered.
					for actorIDint := 0; actorIDint < numActors; actorIDint++ {
						actorID := strconv.Itoa(actorIDint)
						count := countActorAction(respOne, actorID, reminderName)
						count += countActorAction(respTwo, actorID, reminderName)
						// Due to possible load stress, we do not expect all reminders to be called at the same frequency.
						// There are other E2E tests that validate the correct frequency of reminders in a happy path.
						if count == 0 {
							return fmt.Errorf("Reminder %s for Actor %s was invoked %d times with partion count as %d.",
								reminderName, actorID, count, newPartitionCount)
						}
					}
					t.Logf("All reminders triggerred with partition count as %d!", newPartitionCount)
					return nil
				},
				backoff.WithMaxRetries(bo, maxRetries),
				func(err error, d time.Duration) {
					log.Printf("Error while invoking actor: '%v' - retrying in %s", err, d)
				},
			)
			require.NoError(t, err)
		}

		for actorIDint := 0; actorIDint < numActors; actorIDint++ {
			_, err := utils.HTTPGetNTimes(externalURLOne, numHealthChecks)
			require.NoError(t, err)

			actorID := strconv.Itoa(actorIDint)
			// Unregistering reminder
			t.Logf("Unregistering reminder: %s %s ...", actorID, reminderName)
			_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURLOne, actorID, "reminders", reminderName))
			require.NoError(t, err)
		}

		t.Log("Done.")
	})
}
