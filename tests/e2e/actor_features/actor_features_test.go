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

package features

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	guuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	appName                               = "actorfeatures"                      // App name in Dapr.
	reminderName                          = "myReminder"                         // Reminder name.
	timerName                             = "myTimer"                            // Timer name.
	numHealthChecks                       = 60                                   // Number of get calls before starting tests.
	secondsToCheckTimerAndReminderResult  = 20                                   // How much time to wait to make sure the result is in logs.
	secondsToCheckGetMetadata             = 40                                   // How much time to wait to check metadata.
	secondsBetweenChecksForActorFailover  = 5                                    // How much time to wait to make sure the result is in logs.
	minimumCallsForTimerAndReminderResult = 10                                   // How many calls to timer or reminder should be at minimum.
	actorsToCheckRebalance                = 30                                   // How many actors to create in the rebalance check test.
	appScaleToCheckRebalance              = 2                                    // How many instances of the app to create to validate rebalance.
	actorsToCheckMetadata                 = 5                                    // How many actors to create in get metdata test.
	appScaleToCheckMetadata               = 1                                    // How many instances of the app to test get metadata.
	actorInvokeURLFormat                  = "%s/test/testactorfeatures/%s/%s/%s" // URL to invoke a Dapr's actor method in test app.
	actorDeleteURLFormat                  = "%s/actors/testactorfeatures/%s"     // URL to deactivate an actor in test app.
	actorlogsURLFormat                    = "%s/test/logs"                       // URL to fetch logs from test app.
	actorMetadataURLFormat                = "%s/test/metadata"                   // URL to fetch metadata from test app.
	shutdownURLFormat                     = "%s/test/shutdown"                   // URL to shutdown sidecar and app.
	actorInvokeRetriesAfterRestart        = 10                                   // Number of retried to invoke actor after restart.
)

// represents a response for the APIs in this app.
type actorLogEntry struct {
	Action         string `json:"action,omitempty"`
	ActorType      string `json:"actorType,omitempty"`
	ActorID        string `json:"actorId,omitempty"`
	StartTimestamp int    `json:"startTimestamp,omitempty"`
	EndTimestamp   int    `json:"endTimestamp,omitempty"`
}

type activeActorsCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type metadata struct {
	ID     string              `json:"id"`
	Actors []activeActorsCount `json:"actors"`
}

type actorReminderOrTimer struct {
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

func findActorAction(resp []byte, actorID string, action string) *actorLogEntry {
	return findNthActorAction(resp, actorID, action, 1)
}

// findNthActorAction scans the logs and returns the n-th match for the given actorID & method.
func findNthActorAction(resp []byte, actorID string, action string, position int) *actorLogEntry {
	skips := position - 1
	logEntries := parseLogEntries(resp)
	for i := len(logEntries) - 1; i >= 0; i-- {
		logEntry := logEntries[i]
		if (logEntry.ActorID == actorID) && (logEntry.Action == action) {
			if skips == 0 {
				return &logEntry
			}

			skips--
		}
	}

	return nil
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("actor_features")
	utils.InitHTTPClient(true)

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:             appName,
			DaprEnabled:         true,
			DebugLoggingEnabled: true,
			ImageName:           "e2e-actorfeatures",
			Replicas:            1,
			IngressEnabled:      true,
			MetricsEnabled:      true,
			DaprCPULimit:        "2.0",
			DaprCPURequest:      "0.1",
			AppCPULimit:         "2.0",
			AppCPURequest:       "0.1",
		},
		{
			AppName:             "actortestclient",
			DaprEnabled:         true,
			DebugLoggingEnabled: true,
			ImageName:           "e2e-actorclientapp",
			Replicas:            1,
			IngressEnabled:      true,
			MetricsEnabled:      true,
			DaprCPULimit:        "2.0",
			DaprCPURequest:      "0.1",
			AppCPULimit:         "2.0",
			AppCPURequest:       "0.1",
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorInvocation(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL("actortestclient")
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err, "error while waiting for app actortestclient")

	t.Run("Actor remote invocation", func(t *testing.T) {
		actorID := guuid.New().String()

		res, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "testmethod"), []byte{})
		if err != nil {
			log.Printf("failed to invoke method testmethod. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to invoke testmethod")
	})
}

func urlWithReqID(str string, a ...interface{}) string {
	if len(a) > 0 {
		str = fmt.Sprintf(str, a...)
	}
	str = utils.SanitizeHTTPURL(str)
	u, _ := url.Parse(str)
	qs := u.Query()
	qs.Add("reqid", guuid.New().String())
	u.RawQuery = qs.Encode()
	return u.String()
}

func httpGet(url string) ([]byte, error) {
	url = urlWithReqID(url)
	start := time.Now()
	res, err := utils.HTTPGet(url)
	dur := time.Now().Sub(start)
	if err != nil {
		log.Printf("GET %s ERROR after %s: %v", url, dur, err)
		return nil, err
	}
	log.Printf("GET %s completed in %s", url, dur)
	return res, nil
}

func httpPost(url string, data []byte) ([]byte, error) {
	url = urlWithReqID(url)
	start := time.Now()
	res, err := utils.HTTPPost(url, data)
	dur := time.Now().Sub(start)
	if err != nil {
		log.Printf("POST %s ERROR after %s: %v", url, dur, err)
		return nil, err
	}
	log.Printf("POST %s completed in %s", url, dur)
	return res, nil
}

func httpDelete(url string) ([]byte, error) {
	url = urlWithReqID(url)
	start := time.Now()
	res, err := utils.HTTPDelete(url)
	dur := time.Now().Sub(start)
	if err != nil {
		log.Printf("DELETE %s ERROR after %s: %v", url, dur, err)
		return nil, err
	}
	log.Printf("DELETE %s completed in %s", url, dur)
	return res, nil
}

func TestActorFeatures(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	logsURL := fmt.Sprintf(actorlogsURLFormat, externalURL)

	var err error
	var res []byte
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err, "error while waiting for app "+appName)

	t.Run("Actor state.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := guuid.New().String()

		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "savestatetest"), []byte{})
		if err != nil {
			log.Printf("failed to invoke method savestatetest. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to invoke method savestatetest")

		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "getstatetest"), []byte{})
		if err != nil {
			log.Printf("failed to invoke method getstatetest. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to invoke method getstatetest")

		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "savestatetest2"), []byte{})
		if err != nil {
			log.Printf("failed to invoke method savestatetest2. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to invoke method savestatetest2")

		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "getstatetest2"), []byte{})
		if err != nil {
			log.Printf("failed to invoke method getstatetest2. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to invoke method getstatetest2")
	})

	t.Run("Actor reminder.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001"

		// Reset reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "1s",
		}
		var reqBody []byte
		reqBody, err = json.Marshal(req)
		require.NoError(t, err, "failed to marshal JSON")
		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		if err != nil {
			log.Printf("failed to set reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to set reminder")

		time.Sleep(secondsToCheckTimerAndReminderResult * time.Second)

		// Reset reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")
		count := countActorAction(res, actorID, reminderName)
		require.True(t, count >= 1, "condition failed: %d not >= 1. Response='%s'", count, string(res))
		require.True(t, count >= minimumCallsForTimerAndReminderResult, "condition failed: %d not >= %d. Response='%s'", count, minimumCallsForTimerAndReminderResult, string(res))
	})

	t.Run("Actor single fire reminder.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001a"

		// Reset reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
		}
		reqBody, _ := json.Marshal(req)
		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		if err != nil {
			log.Printf("failed to set reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to set reminder")

		time.Sleep(secondsToCheckTimerAndReminderResult * time.Second)

		// Reset reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")
		require.True(t, countActorAction(res, actorID, reminderName) == 1, "condition failed")
	})

	t.Run("Actor reset reminder.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001b"

		// Reset reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "10s",
		}
		reqBody, _ := json.Marshal(req)
		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		if err != nil {
			log.Printf("failed to set reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to set reminder")

		time.Sleep(3 * time.Second)

		// Reset reminder (before first period trigger)
		req.DueTime = "20s"
		reqBody, _ = json.Marshal(req)
		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		time.Sleep(10 * time.Second)

		// Delete reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to delete reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to delete reminder")

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")
		require.Equalf(t, 1, countActorAction(res, actorID, reminderName), "condition failed: %s", string(res))
	})

	t.Run("Actor reminder with deactivate.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001c"

		// Reset reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to set reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "1s",
		}
		reqBody, _ := json.Marshal(req)
		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		if err != nil {
			log.Printf("failed to set reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to set reminder")

		sleepTime := secondsToCheckTimerAndReminderResult / 2 * time.Second
		time.Sleep(sleepTime)

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")

		firstCount := countActorAction(res, actorID, reminderName)
		// Min call is based off of having a 1s period/due time, the amount of seconds we've waited, and a bit of room for timing.
		require.GreaterOrEqual(t, firstCount, 9, "condition failed")

		_, _ = httpDelete(fmt.Sprintf(actorDeleteURLFormat, externalURL, actorID))

		time.Sleep(sleepTime)

		// Reset reminder
		_, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")
		require.Greater(t, countActorAction(res, actorID, reminderName), firstCount)
		require.GreaterOrEqual(t, countActorAction(res, actorID, reminderName), minimumCallsForTimerAndReminderResult)
	})

	t.Run("Actor reminder with app restart.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001d"

		// Reset reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "1s",
		}
		reqBody, _ := json.Marshal(req)
		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		if err != nil {
			log.Printf("failed to set reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to set reminder")

		sleepTime := secondsToCheckTimerAndReminderResult / 2 * time.Second
		time.Sleep(sleepTime)

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")

		firstCount := countActorAction(res, actorID, reminderName)
		minFirstCount := 9
		// Min call is based off of having a 1s period/due time, the amount of seconds we've waited, and a bit of room for timing.
		require.GreaterOrEqual(t, firstCount, minFirstCount)

		log.Printf("Restarting %s ...", appName)
		err := tr.Platform.Restart(appName)
		require.NoError(t, err)

		// Re-establish port forwarding after app restart, likely connection would be lost to pod.
		// replace externalUrl and Logs URL since the new port will be assigned
		externalURL = tr.Platform.AcquireAppExternalURL(appName)
		require.NotEmpty(t, externalURL, "external URL must not be empty!")

		logsURL = fmt.Sprintf(actorlogsURLFormat, externalURL)

		err = backoff.Retry(func() error {
			time.Sleep(30 * time.Second)
			resp, errb := httpGet(logsURL)
			if errb != nil {
				return errb
			}

			count := countActorAction(resp, actorID, reminderName)
			if count < minimumCallsForTimerAndReminderResult {
				return fmt.Errorf("Not enough reminder calls: %d vs %d", count, minimumCallsForTimerAndReminderResult)
			}

			return nil
		}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 10))
		require.NoError(t, err)

		// Reset reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")
	})

	t.Run("Actor reminder delete self.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001e"

		// Reset reminder
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to reset reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset reminder")

		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "10s",
		}
		var reqBody []byte
		reqBody, err = json.Marshal(req)
		require.NoError(t, err, "failed to marshal JSON")
		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		if err != nil {
			log.Printf("failed to set reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to set reminder")

		time.Sleep(secondsToCheckTimerAndReminderResult * time.Second)

		// get reminder
		res, err = httpGet(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		if err != nil {
			log.Printf("failed to get reminder. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get reminder")
		require.Empty(t, res, "Reminder %s exist", reminderName)

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")
		count := countActorAction(res, actorID, reminderName)
		require.LessOrEqual(t, count, 2, "condition failed: %d not == 1. Response='%s'", count, string(res))
	})

	t.Run("Actor timer.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1002"

		// Activate actor.
		res, err := httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "justToActivate"), []byte{})
		if err != nil {
			log.Printf("warn: failed to activate actor. Error='%v' Response='%s'", err, string(res))
		}

		// Reset timer
		res, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName))
		if err != nil {
			log.Printf("failed to reset timer. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to reset timer")

		// Set timer
		req := actorReminderOrTimer{
			Data:    "timerdata",
			DueTime: "1s",
			Period:  "1s",
		}
		reqBody, _ := json.Marshal(req)
		res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName), reqBody)
		if err != nil {
			log.Printf("failed to set timer. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to set timer")

		time.Sleep(secondsToCheckTimerAndReminderResult * time.Second)

		// Reset timer
		_, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName))
		require.NoError(t, err)

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")
		require.True(t, countActorAction(res, actorID, timerName) >= 1)
		require.True(t, countActorAction(res, actorID, timerName) >= minimumCallsForTimerAndReminderResult)
	})

	t.Run("Actor reset timer.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1002a"

		// Activate actor.
		httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "justToActivate"), []byte{})
		// Reset timer
		_, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName))
		require.NoError(t, err)
		// Set timer
		req := actorReminderOrTimer{
			Data:    "timerdata",
			DueTime: "1s",
			Period:  "5s",
		}
		reqBody, _ := json.Marshal(req)
		_, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName), reqBody)
		require.NoError(t, err)

		time.Sleep(3 * time.Second)

		// Reset timer (before first period trigger)
		req.DueTime = "20s"
		reqBody, _ = json.Marshal(req)
		_, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName), reqBody)

		time.Sleep(10 * time.Second)

		// Delete timer
		_, err = httpDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName))
		require.NoError(t, err)

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")
		require.Equal(t, 1, countActorAction(res, actorID, timerName))
	})

	t.Run("Actor concurrency same actor id.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1003"

		// Invoke method call in Actor.
		err1 := make(chan error, 1)
		err2 := make(chan error, 1)

		go func() {
			_, err := httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "concurrency"), []byte{})
			err1 <- err
		}()
		go func() {
			_, err := httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "concurrency"), []byte{})
			err2 <- err
		}()

		require.NoError(t, <-err1)
		require.NoError(t, <-err2)

		res, err = httpGet(logsURL)
		if err != nil {
			log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
		}
		require.NoError(t, err, "failed to get logs")
		logOne := findNthActorAction(res, actorID, "concurrency", 1)
		logTwo := findNthActorAction(res, actorID, "concurrency", 2)
		require.NotNil(t, logOne)
		require.NotNil(t, logTwo)
		require.True(t, (logOne.StartTimestamp < logOne.EndTimestamp)) // Sanity check on the app response.
		require.True(t, (logTwo.StartTimestamp < logTwo.EndTimestamp)) // Sanity check on the app response.
		startEndTimeCheck := (logOne.StartTimestamp >= logTwo.EndTimestamp) || (logTwo.StartTimestamp >= logOne.EndTimestamp)
		if !startEndTimeCheck {
			log.Printf("failed for start/end time check: logOne.StartTimestamp='%d' logOne.EndTimestamp='%d' logTwo.StartTimestamp='%d' logTwo.EndTimestamp='%d'",
				logOne.StartTimestamp, logOne.EndTimestamp, logTwo.StartTimestamp, logTwo.EndTimestamp)
		}
		require.True(t, startEndTimeCheck)
	})

	t.Run("Actor concurrency different actor ids.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorIDOne := "1004a"
		actorIDTwo := "1004b"

		// Invoke method call in Actors.
		err1 := make(chan error, 1)
		err2 := make(chan error, 1)
		defer close(err1)
		defer close(err2)

		go func() {
			_, err := httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDOne, "method", "concurrency"), []byte{})
			err1 <- err
		}()
		go func() {
			_, err := httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDTwo, "method", "concurrency"), []byte{})
			err2 <- err
		}()

		require.NoError(t, <-err1, "error in request 1")
		require.NoError(t, <-err2, "error in request 2")

		// Poll logs with retries because the test app's /test/logs endpoint may not
		// reflect the completed actor calls immediately (eventual consistency).
		var logOne, logTwo *actorLogEntry
		err = backoff.Retry(func() error {
			res, err = httpGet(logsURL)
			if err != nil {
				log.Printf("failed to get logs. Error='%v' Response='%s'", err, string(res))
				return err
			}
			logOne = findActorAction(res, actorIDOne, "concurrency")
			logTwo = findActorAction(res, actorIDTwo, "concurrency")
			if logOne == nil || logTwo == nil {
				return fmt.Errorf("log entries not yet present: logOne=%v logTwo=%v", logOne, logTwo)
			}
			return nil
		}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 10))
		require.NoError(t, err, "timed out waiting for both concurrency log entries")
		require.True(t, (logOne.StartTimestamp < logOne.EndTimestamp)) // Sanity check on the app response.
		require.True(t, (logTwo.StartTimestamp < logTwo.EndTimestamp)) // Sanity check on the app response.
		// Both methods run in parallel, with the sleep time both should start before the other ends.
		startEndTimeCheck := (logOne.StartTimestamp <= logTwo.EndTimestamp) && (logTwo.StartTimestamp <= logOne.EndTimestamp)
		if !startEndTimeCheck {
			log.Printf("failed for start/end time check: logOne.StartTimestamp='%d' logOne.EndTimestamp='%d' logTwo.StartTimestamp='%d' logTwo.EndTimestamp='%d'",
				logOne.StartTimestamp, logOne.EndTimestamp, logTwo.StartTimestamp, logTwo.EndTimestamp)
		}
		require.True(t, startEndTimeCheck)
	})

	t.Run("Actor fails over to another hostname.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1005"

		quit := make(chan struct{})
		go func() {
			// Keep the actor alive
			timer := time.NewTimer(secondsBetweenChecksForActorFailover * time.Second)
			defer timer.Stop()
			for {
				select {
				case <-quit:
					return
				case <-timer.C:
					// Ignore errors as this is just to keep the actor alive
					_, _ = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "hostname"), []byte{})
				}
			}
		}()

		// In Kubernetes, hostname should be the POD name. Single-node Kubernetes cluster should still be able to reproduce this test.
		firstHostname, err := httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "hostname"), []byte{})
		require.NoError(t, err, "error getting first hostname")

		tr.Platform.Restart(appName)

		// Re-establish port forwarding after app restart, likely connection would be lost to pod.
		// replace externalUrl and Logs URL since the new port will be assigned
		externalURL = tr.Platform.AcquireAppExternalURL(appName)
		require.NotEmpty(t, externalURL, "external URL must not be empty!")

		logsURL = fmt.Sprintf(actorlogsURLFormat, externalURL)

		newHostname := []byte{}
		for i := 0; i <= actorInvokeRetriesAfterRestart; i++ {
			// wait until actors are redistributed.
			time.Sleep(30 * time.Second)

			newHostname, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "hostname"), []byte{})
			if i == actorInvokeRetriesAfterRestart {
				require.NoError(t, err, "error getting hostname")
			}

			if err == nil {
				break
			}
		}

		require.NotEqual(t, string(firstHostname), string(newHostname))
		close(quit)
	})

	t.Run("Actor rebalance to another hostname.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorIDBase := "1006Instance"

		var hostnameForActor [actorsToCheckRebalance]string

		// In Kubernetes, hostname should be the POD name.
		// Records all hostnames from pods and compare them with the hostnames from new pods after scaling
		for index := 0; index < actorsToCheckRebalance; index++ {
			hostname, err := httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDBase+strconv.Itoa(index), "method", "hostname"), []byte{})
			require.NoError(t, err)
			hostnameForActor[index] = string(hostname)
		}

		tr.Platform.Scale(appName, appScaleToCheckRebalance)

		// wait until actors are redistributed.
		time.Sleep(30 * time.Second)

		anyActorMoved := false
		for index := 0; index < actorsToCheckRebalance; index++ {
			hostname, err := httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDBase+strconv.Itoa(index), "method", "hostname"), []byte{})
			require.NoError(t, err, "error getting hostname for actor %d", index)

			if hostnameForActor[index] != string(hostname) {
				anyActorMoved = true
			}
		}

		require.True(t, anyActorMoved, "no actor moved")
	})

	t.Run("Get actor metadata", func(t *testing.T) {
		tr.Platform.Scale(appName, appScaleToCheckMetadata)
		time.Sleep(secondsToCheckGetMetadata * time.Second)

		res, err = httpGet(fmt.Sprintf(actorMetadataURLFormat, externalURL))
		log.Printf("Got metadata: Error='%v' Response='%s'", err, string(res))
		require.NoError(t, err, "failed to get metadata")

		var previousMetadata metadata
		err = json.Unmarshal(res, &previousMetadata)
		require.NoError(t, err, "error marshalling to JSON")
		require.NotNil(t, previousMetadata, "previousMetadata object is nil")

		// Each test needs to have a different actorID
		actorIDBase := "1008Instance"

		for index := 0; index < actorsToCheckMetadata; index++ {
			res, err = httpPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDBase+strconv.Itoa(index), "method", "hostname"), []byte{})
			if err != nil {
				log.Printf("failed to check metadata for actor %d. Error='%v' Response='%s'", index, err, string(res))
			}
			require.NoError(t, err, "failed to check metadata for actor %d", index)
		}

		res, err = httpGet(fmt.Sprintf(actorMetadataURLFormat, externalURL))
		log.Printf("Got metadata: Error='%v' Response='%s'", err, string(res))
		require.NoError(t, err, "failed to get metadata")

		var currentMetadata metadata
		err = json.Unmarshal(res, &currentMetadata)
		require.NoError(t, err, "error unmarshalling JSON")
		assert.NotNil(t, currentMetadata, "metadata object is nil")

		assert.Equal(t, appName, currentMetadata.ID)
		assert.Equal(t, appName, previousMetadata.ID)
		assert.Greater(t, len(previousMetadata.Actors), 0)
		assert.Greater(t, len(currentMetadata.Actors), 0)
		assert.Equal(t, "testactorfeatures", currentMetadata.Actors[0].Type)
		assert.Equal(t, "testactorfeatures", previousMetadata.Actors[0].Type)
		assert.Greater(t, currentMetadata.Actors[0].Count, previousMetadata.Actors[0].Count)
	})
}
