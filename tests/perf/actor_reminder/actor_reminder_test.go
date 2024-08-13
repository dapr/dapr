//go:build perf
// +build perf

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

package actor_reminder_perf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/perf"
	"github.com/dapr/dapr/tests/perf/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/dapr/dapr/tests/runner/summary"
	"github.com/dapr/kit/ptr"
)

const (
	numHealthChecks    = 60 // Number of times to check for endpoint health per app.
	actorType          = "PerfTestActorReminder"
	actorTypeScheduler = "PerfTestActorReminderScheduler"
	appName            = "perf-actor-reminder-service"
	appNameScheduler   = "perf-actor-reminder-scheduler-service"

	// Target for the QPS - Temporary
	targetQPS          float64 = 30
	targetSchedulerQPS float64 = 2000

	// Reminders repetition count and interval, used to calculate the target trigger QPS
	repeatCount           = 5
	repeatIntervalSeconds = 1

	// Target for the QPS to trigger reminders.
	targetTriggerQPS          float64 = 950
	targetSchedulerTriggerQPS float64 = 1500

	// reminderCount is the number of reminders to register.
	// actor reminders bottlenecks at 2500 due to serialization
	// at this point there's a risk of timeouts during reminder creation
	// using smaller number of reminders for the registration and trigger test to be consistent with the numbers
	reminderCount          = 1000
	reminderCountScheduler = 50000

	// dueTime is the time in seconds to execute the reminders. This covers the
	// time to register the reminders and the time to trigger them.
	dueTime          = 180
	dueTimeScheduler = 330
)

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("actor_reminder")

	testApps := []kube.AppDescription{
		{
			AppName:           appName,
			DaprEnabled:       true,
			ImageName:         "perf-actorfeatures",
			Replicas:          1,
			IngressEnabled:    true,
			AppPort:           3000,
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "1.0",
			DaprMemoryLimit:   "2Gi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "1.0",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
			AppEnv: map[string]string{
				"TEST_APP_ACTOR_TYPE": actorType,
			},
			EnableProfiling: false, // Enable profiling when running locally
		},
		{
			AppName:           appNameScheduler,
			DaprEnabled:       true,
			ImageName:         "perf-actorfeatures",
			Replicas:          1,
			IngressEnabled:    true,
			AppPort:           3000,
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "1.0",
			DaprMemoryLimit:   "2Gi",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "1.0",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
			AppEnv: map[string]string{
				"TEST_APP_ACTOR_TYPE": actorTypeScheduler,
			},
			Config:          "featureactorreminderscheduler",
			EnableProfiling: false,
		},
	}

	tr = runner.NewTestRunner("actorreminder", testApps, nil, nil)
	os.Exit(tr.Start(m))
}

func TestActorReminderRegistrationPerformance(t *testing.T) {
	p := perf.Params(
		perf.WithQPS(500),
		perf.WithConnections(8),
		perf.WithDuration("1m"),
		perf.WithPayload("{}"),
	)

	var testAppURL string
	if tr.Platform.IsLocalRun() {
		ports, err := tr.Platform.PortForwardToApp(appName, 3000)
		require.NoError(t, err)

		t.Logf("Ports: %v", ports)

		appPort := ports[0]
		testAppURL = "localhost:" + strconv.Itoa(appPort)
	} else {
		testAppURL = tr.Platform.AcquireAppExternalURL(appName)
	}

	tr.Platform.ProfileApp(appName, p.TestDuration)

	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	// Cleanup previous reminders
	cleanupResponse, err := utils.HTTPGet(testAppURL + "/test/cleanup/" + actorType)
	require.NoError(t, err)
	require.NotEmpty(t, cleanupResponse)
	t.Logf("reminders cleanup results: %s", string(cleanupResponse))

	t.Logf("Waiting 5s after cleanup")
	time.Sleep(5 * time.Second)

	// Perform dapr test
	endpoint := fmt.Sprintf("http://127.0.0.1:3500/v1.0/actors/%v/{uuid}/reminders/myreminder", actorType)
	p.TargetEndpoint = endpoint
	p.Payload = `{"dueTime":"24h","period":"24h"}`
	body, err := json.Marshal(&p)
	require.NoError(t, err)

	t.Logf("running dapr test with params: %s", body)
	daprResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testAppURL), body)
	t.Log("checking err...")
	require.NoError(t, err)
	require.NotEmpty(t, daprResp)
	// fast fail if daprResp starts with error
	require.False(t, strings.HasPrefix(string(daprResp), "error"))

	// Let test run for 90s triggering the timers and collect metrics.
	time.Sleep(90 * time.Second)

	appUsage, err := tr.Platform.GetAppUsage(appName)
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage(appName)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(appName)
	require.NoError(t, err)

	t.Logf("dapr test results: %s", string(daprResp))
	t.Logf("target dapr app consumed %vm CPU and %vMb of Memory", appUsage.CPUm, appUsage.MemoryMb)
	t.Logf("target dapr sidecar consumed %vm CPU and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)
	t.Logf("target dapr app or sidecar restarted %v times", restarts)

	var daprResult perf.TestResult
	err = json.Unmarshal(daprResp, &daprResult)
	require.NoErrorf(t, err, "Failed to unmarshal: %s", string(daprResp))

	percentiles := map[int]string{2: "90th", 3: "99th"}

	for k, v := range percentiles {
		daprValue := daprResult.DurationHistogram.Percentiles[k].Value
		t.Logf("%s percentile: %sms", v, fmt.Sprintf("%.2f", daprValue*1000))
	}
	t.Logf("Actual QPS: %.2f, expected QPS: %f", daprResult.ActualQPS, targetQPS) // TODO: Revert to p.QPS

	summary.ForTest(t).
		Service(appName).
		Client(appName).
		CPU(appUsage.CPUm).
		Memory(appUsage.MemoryMb).
		SidecarCPU(sidecarUsage.CPUm).
		SidecarMemory(sidecarUsage.MemoryMb).
		Restarts(restarts).
		ActualQPS(daprResult.ActualQPS).
		Params(p).
		OutputFortio(daprResult).
		Flush()

	assert.Equal(t, 0, daprResult.RetCodes.Num400)
	assert.Equal(t, 0, daprResult.RetCodes.Num500)
	assert.Equal(t, 0, restarts)
	assert.True(t, daprResult.ActualQPS > targetQPS) // TODO: Revert to p.QPS
}

func TestActorReminderSchedulerRegistrationPerformance(t *testing.T) {
	p := perf.Params(
		perf.WithQPS(5000),
		perf.WithConnections(8),
		perf.WithDuration("1m"),
		perf.WithPayload("{}"),
	)

	var testAppURL string
	if tr.Platform.IsLocalRun() {
		ports, err := tr.Platform.PortForwardToApp(appNameScheduler, 3000)
		require.NoError(t, err)

		t.Logf("Ports: %v", ports)

		appPort := ports[0]
		testAppURL = "localhost:" + strconv.Itoa(appPort)
	} else {
		testAppURL = tr.Platform.AcquireAppExternalURL(appNameScheduler)
	}
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	// Cleanup previous reminders
	cleanupResponse, err := utils.HTTPGet(testAppURL + "/test/cleanup/" + actorTypeScheduler)
	require.NoError(t, err)
	require.NotEmpty(t, cleanupResponse)
	t.Logf("reminders cleanup results: %s", string(cleanupResponse))

	t.Logf("Waiting 5s after cleanup")
	time.Sleep(5 * time.Second)

	tr.Platform.ProfileApp(appNameScheduler, p.TestDuration)

	// Perform dapr test
	endpoint := fmt.Sprintf("http://127.0.0.1:3500/v1.0/actors/%v/{uuid}/reminders/myreminder", actorTypeScheduler)
	p.TargetEndpoint = endpoint
	p.Payload = `{"dueTime":"24h","period":"24h"}`
	body, err := json.Marshal(&p)
	require.NoError(t, err)

	t.Logf("running dapr test with params: %s", body)
	daprResp, err := utils.HTTPPost(fmt.Sprintf("%s/test", testAppURL), body)
	t.Log("checking err...")
	require.NoError(t, err)
	require.NotEmpty(t, daprResp)
	// fast fail if daprResp starts with error
	require.False(t, strings.HasPrefix(string(daprResp), "error"))

	// Let test run for 90s triggering the timers and collect metrics.
	time.Sleep(90 * time.Second)

	appUsage, err := tr.Platform.GetAppUsage(appNameScheduler)
	require.NoError(t, err)

	sidecarUsage, err := tr.Platform.GetSidecarUsage(appNameScheduler)
	require.NoError(t, err)

	restarts, err := tr.Platform.GetTotalRestarts(appNameScheduler)
	require.NoError(t, err)

	t.Logf("dapr test results: %s", string(daprResp))
	t.Logf("target dapr app consumed %vm CPU and %vMb of Memory", appUsage.CPUm, appUsage.MemoryMb)
	t.Logf("target dapr sidecar consumed %vm CPU and %vMb of Memory", sidecarUsage.CPUm, sidecarUsage.MemoryMb)
	t.Logf("target dapr app or sidecar restarted %v times", restarts)

	var daprResult perf.TestResult
	err = json.Unmarshal(daprResp, &daprResult)
	require.NoErrorf(t, err, "Failed to unmarshal: %s", string(daprResp))

	percentiles := map[int]string{2: "90th", 3: "99th"}

	for k, v := range percentiles {
		daprValue := daprResult.DurationHistogram.Percentiles[k].Value
		t.Logf("%s percentile: %sms", v, fmt.Sprintf("%.2f", daprValue*1000))
	}

	t.Logf("Actual QPS: %.2f, expected QPS: %.0f", daprResult.ActualQPS, targetSchedulerQPS)

	summary.ForTest(t).
		Service(appNameScheduler).
		Client(appNameScheduler).
		CPU(appUsage.CPUm).
		Memory(appUsage.MemoryMb).
		SidecarCPU(sidecarUsage.CPUm).
		SidecarMemory(sidecarUsage.MemoryMb).
		Restarts(restarts).
		ActualQPS(daprResult.ActualQPS).
		Params(p).
		OutputFortio(daprResult).
		Flush()

	assert.Equal(t, 0, daprResult.RetCodes.Num400)
	assert.Equal(t, 0, daprResult.RetCodes.Num500)
	assert.Equal(t, 0, restarts)
	assert.GreaterOrEqual(t, daprResult.ActualQPS, targetSchedulerQPS)
}

type actorReminderRequest struct {
	DueTime *string `json:"dueTime,omitempty"`
	Period  *string `json:"period,omitempty"`
	Ttl     *string `json:"ttl,omitempty"`
}

func TestActorReminderTriggerPerformance(t *testing.T) {
	var testAppURL string
	if tr.Platform.IsLocalRun() {
		ports, err := tr.Platform.PortForwardToApp(appName, 3000)
		require.NoError(t, err)

		t.Logf("Ports: %v", ports)

		appPort := ports[0]
		testAppURL = "localhost:" + strconv.Itoa(appPort)
	} else {
		testAppURL = tr.Platform.AcquireAppExternalURL(appName)
	}
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	// Cleanup previous reminders
	cleanupResponse, err := utils.HTTPGet(testAppURL + "/test/cleanup/" + actorType)
	require.NoError(t, err)
	require.NotEmpty(t, cleanupResponse)
	t.Logf("reminders cleanup results: %s", string(cleanupResponse))

	t.Logf("Waiting 5s after cleanup")
	time.Sleep(5 * time.Second)

	t.Logf("invoking actor reminder")
	_, err = utils.HTTPGet(fmt.Sprintf("%s/actors/%s/abc/method/foo", testAppURL, actorType))
	require.NoError(t, err)

	t.Logf("registering actor reminders")
	reminder := &actorReminderRequest{
		DueTime: ptr.Of(time.Now().Add(time.Second * dueTime).Format(time.RFC3339)),
		Period:  ptr.Of(fmt.Sprintf("R%d/PT%dS", repeatCount, repeatIntervalSeconds)),
	}
	reminderB, err := json.Marshal(reminder)
	require.NoError(t, err)

	worker := func(i int) {
		_, err = utils.HTTPPost(fmt.Sprintf("%s/actors/%s/abc/reminders/myreminder%d", testAppURL, actorType, i), reminderB)
		require.NoError(t, err)

		if (i+1)%100 == 0 {
			fmt.Printf("Reminders registered: %d\n", i+1)
		}
	}

	wg := sync.WaitGroup{}
	ch := make(chan int)
	for j := 0; j < 50; j++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				i, ok := <-ch
				if !ok {
					return
				}
				worker(i)
			}
		}()
	}

	now := time.Now()
	for i := 0; i < reminderCount; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
	done := time.Since(now)
	t.Logf("Created %d reminders in %s (%.1fqps)", reminderCount, done, float64(reminderCount)/done.Seconds())

	require.GreaterOrEqualf(t, dueTime*time.Second, done, "expected to create reminders %d in less than %ds", reminderCount, dueTime)

	t.Logf("Waiting %s for reminders to trigger", (dueTime*time.Second)-done)
	time.Sleep((dueTime * time.Second) - done)

	t.Logf("Waiting for %d reminders to trigger", reminderCount*repeatCount)
	triggeredCount := 0
	start := time.Now()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := utils.HTTPGet(fmt.Sprintf("%s/remindersCount", testAppURL))
		require.NoError(t, err)
		triggeredCount, err = strconv.Atoi(strings.TrimSpace(string(resp)))
		require.NoError(t, err)
		assert.GreaterOrEqual(c, triggeredCount, reminderCount*repeatCount)
	}, 100*time.Second, 100*time.Millisecond)

	done = time.Since(start)
	qps := float64(triggeredCount) / done.Seconds()
	t.Logf("Triggered %d reminders in %s (%.1fqps)", triggeredCount, done, qps)
	assert.GreaterOrEqual(t, qps, targetTriggerQPS)

	// Allow reminders to be cleaned by dapr
	time.Sleep(15 * time.Second)
}

func TestActorReminderSchedulerTriggerPerformance(t *testing.T) {
	var testAppURL string
	if tr.Platform.IsLocalRun() {
		ports, err := tr.Platform.PortForwardToApp(appNameScheduler, 3000)
		require.NoError(t, err)

		t.Logf("Ports: %v", ports)

		appPort := ports[0]
		testAppURL = "localhost:" + strconv.Itoa(appPort)
	} else {
		testAppURL = tr.Platform.AcquireAppExternalURL(appNameScheduler)
	}
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	// Cleanup previous reminders
	cleanupResponse, err := utils.HTTPGet(testAppURL + "/test/cleanup/" + actorTypeScheduler)
	require.NoError(t, err)
	require.NotEmpty(t, cleanupResponse)
	t.Logf("reminders cleanup results: %s", string(cleanupResponse))

	t.Logf("Waiting 30s after cleanup")
	time.Sleep(30 * time.Second)

	t.Logf("invoking actor reminder scheduler")
	_, err = utils.HTTPGet(fmt.Sprintf("%s/actors/%s/abc/method/foo", testAppURL, actorTypeScheduler))
	require.NoError(t, err)

	t.Logf("registering actor reminders")
	reminder := &actorReminderRequest{
		DueTime: ptr.Of(time.Now().Add(time.Second * dueTimeScheduler).Format(time.RFC3339)),
		Period:  ptr.Of(fmt.Sprintf("R%d/PT%dS", repeatCount, repeatIntervalSeconds)),
	}
	reminderB, err := json.Marshal(reminder)
	require.NoError(t, err)

	worker := func(i int) {
		err := backoff.Retry(func() error {
			_, err = utils.HTTPPost(fmt.Sprintf("%s/actors/%s/abc/reminders/myreminder%d", testAppURL, actorTypeScheduler, i), reminderB)
			return err
		}, backoff.WithMaxRetries(backoff.NewConstantBackOff(1*time.Second), 20))
		require.NoError(t, err)

		if (i+1)%10000 == 0 {
			fmt.Printf("Reminders registered: %d\n", i+1)
		}
	}

	wg := sync.WaitGroup{}
	ch := make(chan int)
	for j := 0; j < 50; j++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for {
				i, ok := <-ch
				if !ok {
					return
				}
				worker(i)
			}
		}()
	}

	now := time.Now()
	for i := 0; i < reminderCountScheduler; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
	done := time.Since(now)
	t.Logf("Created %d reminders in %s (%.1fqps)", reminderCountScheduler, done, float64(reminderCountScheduler)/done.Seconds())

	require.GreaterOrEqualf(t, dueTimeScheduler*time.Second, done, "expected to create reminders %d in less than %ds", reminderCountScheduler, dueTimeScheduler)

	t.Logf("Waiting %s for reminders to trigger", (dueTimeScheduler*time.Second)-done)
	time.Sleep((dueTimeScheduler * time.Second) - done)

	t.Logf("Waiting for %d reminders to trigger", reminderCountScheduler*repeatCount)
	triggeredCount := 0
	start := time.Now()
	waitFor := 10 * time.Minute
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := utils.HTTPGet(fmt.Sprintf("%s/remindersCount", testAppURL))
		require.NoError(t, err)
		triggeredCount, err = strconv.Atoi(strings.TrimSpace(string(resp)))
		require.NoError(t, err)
		assert.GreaterOrEqual(c, triggeredCount, reminderCountScheduler*repeatCount)
	}, waitFor, 100*time.Millisecond, "expected to trigger %d reminders in less than %.1f seconds", reminderCountScheduler*repeatCount, waitFor.Seconds())
	done = time.Since(start)

	resp, err := utils.HTTPGet(fmt.Sprintf("%s/remindersMap", testAppURL))
	require.NoError(t, err)

	// save reminders map to a file
	logsPath := os.Getenv("DAPR_CONTAINER_LOG_PATH")
	if logsPath == "" {
		logsPath = "./container_logs"
	}
	err = os.WriteFile(filepath.Join(logsPath, "remindersMap.json"), resp, 0644)
	require.NoError(t, err)

	qps := float64(triggeredCount) / done.Seconds()
	t.Logf("Triggered %d reminders in %s (%.1fqps)", triggeredCount, done, qps)

	assert.GreaterOrEqual(t, qps, targetSchedulerTriggerQPS)

	// Allow reminders to be cleaned by dapr
	time.Sleep(15 * time.Second)
}
