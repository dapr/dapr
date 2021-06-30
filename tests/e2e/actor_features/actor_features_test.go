// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package features

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
	secondsToCheckGetMetadata             = 10                                   // How much time to wait to check metadata.
	secondsBetweenChecksForActorFailover  = 5                                    // How much time to wait to make sure the result is in logs.
	minimumCallsForTimerAndReminderResult = 10                                   // How many calls to timer or reminder should be at minimum.
	actorsToCheckRebalance                = 10                                   // How many actors to create in the rebalance check test.
	appScaleToCheckRebalance              = 2                                    // How many instances of the app to create to validate rebalance.
	actorsToCheckMetadata                 = 5                                    // How many actors to create in get metdata test.
	appScaleToCheckMetadata               = 1                                    // How many instances of the app to test get metadata.
	actorInvokeURLFormat                  = "%s/test/testactorfeatures/%s/%s/%s" // URL to invoke a Dapr's actor method in test app.
	actorDeleteURLFormat                  = "%s/actors/testactorfeatures/%s"     // URL to deactivate an actor in test app.
	actorlogsURLFormat                    = "%s/test/logs"                       // URL to fetch logs from test app.
	actorMetadataURLFormat                = "%s/test/metadata"                   // URL to fetch metadata from test app.
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
	for _, logEntry := range logEntries {
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
		},
		{
			AppName:        "actortestclient",
			DaprEnabled:    true,
			ImageName:      "e2e-actorclientapp",
			Replicas:       1,
			IngressEnabled: true,
			MetricsEnabled: true,
			DaprCPULimit:   "2.0",
			DaprCPURequest: "0.1",
			AppCPULimit:    "2.0",
			AppCPURequest:  "0.1",
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
	require.NoError(t, err)

	t.Run("Actor remote invocation", func(t *testing.T) {
		actorID := guuid.New().String()

		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "testmethod"), []byte{})
		require.NoError(t, err)
	})
}

func TestActorFeatures(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	logsURL := fmt.Sprintf(actorlogsURLFormat, externalURL)

	var err error
	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err = utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Run("Actor state.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := guuid.New().String()

		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "savestatetest"), []byte{})
		require.NoError(t, err)

		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "getstatetest"), []byte{})
		require.NoError(t, err)

		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "savestatetest2"), []byte{})
		require.NoError(t, err)

		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "getstatetest2"), []byte{})
		require.NoError(t, err)
	})

	t.Run("Actor reminder.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001"

		// Reset reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)
		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "1s",
		}
		var reqBody []byte
		reqBody, err = json.Marshal(req)
		require.NoError(t, err)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		require.NoError(t, err)

		time.Sleep(secondsToCheckTimerAndReminderResult * time.Second)

		// Reset reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)

		var resp []byte
		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)
		require.True(t, countActorAction(resp, actorID, reminderName) >= 1)
		require.True(t, countActorAction(resp, actorID, reminderName) >= minimumCallsForTimerAndReminderResult)
	})

	t.Run("Actor single fire reminder.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001a"

		// Reset reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)
		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
		}
		reqBody, _ := json.Marshal(req)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		require.NoError(t, err)

		time.Sleep(secondsToCheckTimerAndReminderResult * time.Second)

		// Reset reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)

		var resp []byte
		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)
		require.True(t, countActorAction(resp, actorID, reminderName) == 1)
	})

	t.Run("Actor reset reminder.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001b"

		// Reset reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)
		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "5s",
		}
		reqBody, _ := json.Marshal(req)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		require.NoError(t, err)

		time.Sleep(3 * time.Second)

		// Reset reminder (before first period trigger)
		req.DueTime = "20s"
		reqBody, _ = json.Marshal(req)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)

		time.Sleep(10 * time.Second)

		// Delete reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)

		var resp []byte
		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)
		require.True(t, countActorAction(resp, actorID, reminderName) == 1)
	})

	t.Run("Actor reminder with deactivate.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001c"

		// Reset reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)
		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "1s",
		}
		reqBody, _ := json.Marshal(req)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		require.NoError(t, err)

		sleepTime := secondsToCheckTimerAndReminderResult / 2 * time.Second
		time.Sleep(sleepTime)

		var resp []byte
		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)
		firstCount := countActorAction(resp, actorID, reminderName)
		// Min call is based off of having a 1s period/due time, the amount of seconds we've waited, and a bit of room for timing.
		require.GreaterOrEqual(t, firstCount, 9)

		_, _ = utils.HTTPDelete(fmt.Sprintf(actorDeleteURLFormat, externalURL, actorID))

		time.Sleep(sleepTime)

		// Reset reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)

		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)
		require.Greater(t, countActorAction(resp, actorID, reminderName), firstCount)
		require.GreaterOrEqual(t, countActorAction(resp, actorID, reminderName), minimumCallsForTimerAndReminderResult)
	})

	t.Run("Actor reminder with app restart.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1001d"

		// Reset reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)
		// Set reminder
		req := actorReminderOrTimer{
			Data:    "reminderdata",
			DueTime: "1s",
			Period:  "1s",
		}
		reqBody, _ := json.Marshal(req)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName), reqBody)
		require.NoError(t, err)

		sleepTime := secondsToCheckTimerAndReminderResult / 2 * time.Second
		time.Sleep(sleepTime)

		var resp []byte
		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)
		firstCount := countActorAction(resp, actorID, reminderName)
		minFirstCount := 9
		// Min call is based off of having a 1s period/due time, the amount of seconds we've waited, and a bit of room for timing.
		require.GreaterOrEqual(t, firstCount, minFirstCount)

		err = tr.Platform.Restart(appName)
		assert.NoError(t, err)
		externalURL = tr.Platform.AcquireAppExternalURL(appName)
		require.NotEmpty(t, externalURL, "Could not get external URL after app restart.")

		time.Sleep(sleepTime)

		// Reset reminder
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "reminders", reminderName))
		require.NoError(t, err)

		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)

		restartDelayDiscount := 2
		require.GreaterOrEqual(t, countActorAction(resp, actorID, reminderName), minFirstCount-restartDelayDiscount)
		require.GreaterOrEqual(t, countActorAction(resp, actorID, reminderName), minimumCallsForTimerAndReminderResult-restartDelayDiscount)
	})

	t.Run("Actor timer.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1002"

		// Activate actor.
		utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "justToActivate"), []byte{})
		// Reset timer
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName))
		require.NoError(t, err)
		// Set timer
		req := actorReminderOrTimer{
			Data:    "timerdata",
			DueTime: "1s",
			Period:  "1s",
		}
		reqBody, _ := json.Marshal(req)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName), reqBody)
		require.NoError(t, err)

		time.Sleep(secondsToCheckTimerAndReminderResult * time.Second)

		// Reset timer
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName))
		require.NoError(t, err)

		var resp []byte
		resp, err = utils.HTTPGet(logsURL)
		require.NoError(t, err)
		require.True(t, countActorAction(resp, actorID, timerName) >= 1)
		require.True(t, countActorAction(resp, actorID, timerName) >= minimumCallsForTimerAndReminderResult)
	})

	t.Run("Actor reset timer.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1002a"

		// Activate actor.
		utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "justToActivate"), []byte{})
		// Reset timer
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName))
		require.NoError(t, err)
		// Set timer
		req := actorReminderOrTimer{
			Data:    "timerdata",
			DueTime: "1s",
			Period:  "5s",
		}
		reqBody, _ := json.Marshal(req)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName), reqBody)
		require.NoError(t, err)

		time.Sleep(3 * time.Second)

		// Reset timer (before first period trigger)
		req.DueTime = "20s"
		reqBody, _ = json.Marshal(req)
		_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName), reqBody)

		time.Sleep(10 * time.Second)

		// Delete timer
		_, err = utils.HTTPDelete(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "timers", timerName))
		require.NoError(t, err)

		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)
		require.Equal(t, 1, countActorAction(resp, actorID, timerName))
	})

	t.Run("Actor concurrency same actor id.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1003"

		// Invoke method call in Actor.
		err1 := make(chan error, 1)
		err2 := make(chan error, 1)

		go func() {
			_, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "concurrency"), []byte{})
			err1 <- err
		}()
		go func() {
			_, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "concurrency"), []byte{})
			err2 <- err
		}()

		require.NoError(t, <-err1)
		require.NoError(t, <-err2)

		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)
		logOne := findNthActorAction(resp, actorID, "concurrency", 1)
		logTwo := findNthActorAction(resp, actorID, "concurrency", 2)
		require.NotNil(t, logOne)
		require.NotNil(t, logTwo)
		require.True(t, (logOne.StartTimestamp < logOne.EndTimestamp)) // Sanity check on the app response.
		require.True(t, (logTwo.StartTimestamp < logTwo.EndTimestamp)) // Sanity check on the app response.
		require.True(t, (logOne.StartTimestamp >= logTwo.EndTimestamp) || (logTwo.StartTimestamp >= logOne.EndTimestamp))
	})

	t.Run("Actor concurrency different actor ids.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorIDOne := "1004a"
		actorIDTwo := "1004b"

		// Invoke method call in Actors.
		err1 := make(chan error, 1)
		err2 := make(chan error, 1)

		go func() {
			_, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDOne, "method", "concurrency"), []byte{})
			err1 <- err
		}()
		go func() {
			_, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDTwo, "method", "concurrency"), []byte{})
			err2 <- err
		}()

		require.NoError(t, <-err1)
		require.NoError(t, <-err2)

		resp, err := utils.HTTPGet(logsURL)
		require.NoError(t, err)
		logOne := findActorAction(resp, actorIDOne, "concurrency")
		logTwo := findActorAction(resp, actorIDTwo, "concurrency")
		require.NotNil(t, logOne)
		require.NotNil(t, logTwo)
		require.True(t, (logOne.StartTimestamp < logOne.EndTimestamp)) // Sanity check on the app response.
		require.True(t, (logTwo.StartTimestamp < logTwo.EndTimestamp)) // Sanity check on the app response.
		// Both methods run in parallel, with the sleep time both should start before the other ends.
		require.True(t, (logOne.StartTimestamp <= logTwo.EndTimestamp) && (logTwo.StartTimestamp <= logOne.EndTimestamp))
	})

	t.Run("Actor fails over to another hostname.", func(t *testing.T) {
		// Each test needs to have a different actorID
		actorID := "1005"

		quit := make(chan struct{})
		go func() {
			for {
				<-quit

				_, backgroundError := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "hostname"), []byte{})
				require.NoError(t, backgroundError)
				time.Sleep(secondsBetweenChecksForActorFailover * time.Second)
			}
		}()

		// In Kubernetes, hostname should be the POD name. Single-node Kubernetes cluster should still be able to reproduce this test.
		firstHostname, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "hostname"), []byte{})
		require.NoError(t, err)

		tr.Platform.Restart(appName)

		newHostname := []byte{}
		for i := 0; i <= actorInvokeRetriesAfterRestart; i++ {
			// wait until actors are redistributed.
			time.Sleep(30 * time.Second)

			newHostname, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorID, "method", "hostname"), []byte{})
			if i == actorInvokeRetriesAfterRestart {
				require.NoError(t, err)
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
			hostname, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDBase+strconv.Itoa(index), "method", "hostname"), []byte{})
			require.NoError(t, err)
			hostnameForActor[index] = string(hostname)
		}

		tr.Platform.Scale(appName, appScaleToCheckRebalance)

		// wait until actors are redistributed.
		time.Sleep(30 * time.Second)

		anyActorMoved := false
		for index := 0; index < actorsToCheckRebalance; index++ {
			hostname, err := utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDBase+strconv.Itoa(index), "method", "hostname"), []byte{})
			require.NoError(t, err)

			if hostnameForActor[index] != string(hostname) {
				anyActorMoved = true
			}
		}

		require.True(t, anyActorMoved)
	})

	t.Run("Get actor metadata", func(t *testing.T) {
		tr.Platform.Scale(appName, appScaleToCheckMetadata)
		time.Sleep(secondsToCheckGetMetadata * time.Second)

		res, err := utils.HTTPGet(fmt.Sprintf(actorMetadataURLFormat, externalURL))
		require.NoError(t, err)

		var prevMetadata metadata
		err = json.Unmarshal(res, &prevMetadata)
		require.NoError(t, err)
		var prevActors int
		if len(prevMetadata.Actors) > 0 {
			prevActors = prevMetadata.Actors[0].Count
		}

		// Each test needs to have a different actorID
		actorIDBase := "1008Instance"

		for index := 0; index < actorsToCheckMetadata; index++ {
			_, err = utils.HTTPPost(fmt.Sprintf(actorInvokeURLFormat, externalURL, actorIDBase+strconv.Itoa(index), "method", "hostname"), []byte{})
			require.NoError(t, err)
		}

		// Add delay to make the test robust.
		time.Sleep(5 * time.Second)

		res, err = utils.HTTPGet(fmt.Sprintf(actorMetadataURLFormat, externalURL))
		require.NoError(t, err)

		expected := metadata{
			ID: appName,
			Actors: []activeActorsCount{{
				Type:  "testactorfeatures",
				Count: prevActors + actorsToCheckMetadata,
			}},
		}
		var actual metadata
		err = json.Unmarshal(res, &actual)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	})
}
