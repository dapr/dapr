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
	"strconv"
	"strings"
	"testing"
	"time"

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
	actorTypeScheduler = "PerfTestActorReminderSchduler"
	appName            = "perf-actor-reminder-service"
	appNameScheduler   = "perf-actor-reminder-scheduler-service"

	// Target for the QPS
	targetQPS          float64 = 56
	targetSchedulerQPS float64 = 2850

	// Target for the QPS to trigger reminders.
	targetTriggerQPS          float64 = 4700
	targetSchedulerTriggerQPS float64 = 3800

	// reminderCount is the number of reminders to register.
	reminderCount          = 5000
	reminderCountScheduler = 50000

	// dueTime is the time in seconds to execute the reminders. This covers the
	// time to register the reminders and the time to trigger them.
	dueTime          = 90
	dueTimeScheduler = 25
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
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "2GiB",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
			AppEnv: map[string]string{
				"TEST_APP_ACTOR_TYPE": actorType,
			},
		},
		{
			AppName:           appNameScheduler,
			DaprEnabled:       true,
			ImageName:         "perf-actorfeatures",
			Replicas:          1,
			IngressEnabled:    true,
			AppPort:           3000,
			DaprCPULimit:      "4.0",
			DaprCPURequest:    "0.1",
			DaprMemoryLimit:   "2GiB",
			DaprMemoryRequest: "250Mi",
			AppCPULimit:       "4.0",
			AppCPURequest:     "0.1",
			AppMemoryLimit:    "800Mi",
			AppMemoryRequest:  "2500Mi",
			AppEnv: map[string]string{
				"TEST_APP_ACTOR_TYPE": actorTypeScheduler,
			},
			Config: "featureactorreminderscheduler",
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

	// Get the ingress external url of test app
	testAppURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

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
	t.Logf("Actual QPS: %.2f, expected QPS: %d", daprResult.ActualQPS, p.QPS)

	report := perf.NewTestReport(
		[]perf.TestResult{daprResult},
		"Actor Reminder",
		sidecarUsage,
		appUsage)
	report.SetTotalRestartCount(restarts)
	err = utils.UploadAzureBlob(report)

	if err != nil {
		t.Error(err)
	}

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
	assert.True(t, daprResult.ActualQPS > targetQPS)
}

func TestActorReminderSchedulerRegistrationPerformance(t *testing.T) {
	p := perf.Params(
		perf.WithQPS(5000),
		perf.WithConnections(8),
		perf.WithDuration("1m"),
		perf.WithPayload("{}"),
	)

	// Get the ingress external url of test app
	testAppURL := tr.Platform.AcquireAppExternalURL(appNameScheduler)
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

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

	report := perf.NewTestReport(

		[]perf.TestResult{daprResult},
		"Actor Reminder",
		sidecarUsage,
		appUsage)

	report.SetTotalRestartCount(restarts)
	err = utils.UploadAzureBlob(report)

	if err != nil {
		t.Error(err)
	}

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
	// Get the ingress external url of test app
	testAppURL := tr.Platform.AcquireAppExternalURL(appName)
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	t.Logf("invoking actor reminder scheduler")
	_, err = utils.HTTPGet(fmt.Sprintf("%s/actors/%s/abc/method/foo", testAppURL, actorType))
	require.NoError(t, err)

	t.Logf("registering actor reminders")
	reminder := &actorReminderRequest{
		DueTime: ptr.Of(time.Now().Add(time.Second * dueTime).Format(time.RFC3339)),
		Period:  ptr.Of("1s"),
		Ttl:     ptr.Of("5s"),
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

	ch := make(chan int)
	for j := 0; j < 50; j++ {
		go func() {
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
	done := time.Since(now)
	t.Logf("Created %d reminders in %s (%.1fqps)", reminderCount, done, float64(reminderCount)/done.Seconds())

	require.GreaterOrEqualf(t, dueTime*time.Second, done, "expected to create reminders %d in less than %ds", reminderCount, dueTime)

	t.Logf("Waiting %s for reminders to trigger", (dueTime*time.Second)-done)
	time.Sleep((dueTime * time.Second) - done)

	t.Logf("Waiting for %d reminders to trigger", reminderCount*5)
	start := time.Now()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := utils.HTTPGet(fmt.Sprintf("%s/remindersCount", testAppURL))
		require.NoError(t, err)
		gotCount, err := strconv.Atoi(strings.TrimSpace(string(resp)))
		require.NoError(t, err)
		assert.GreaterOrEqual(c, gotCount, reminderCount*5)
	}, 100*time.Second, time.Millisecond*100)
	done = time.Since(start)
	qps := float64(reminderCount*5) / done.Seconds()
	t.Logf("Triggered %d reminders in %s (%.1fqps)", reminderCount*5, done, qps)
	assert.GreaterOrEqual(t, qps, targetTriggerQPS)
}

func TestActorReminderSchedulerTriggerPerformance(t *testing.T) {
	// Get the ingress external url of test app
	testAppURL := tr.Platform.AcquireAppExternalURL(appNameScheduler)
	require.NotEmpty(t, testAppURL, "test app external URL must not be empty")

	// Check if test app endpoint is available
	t.Logf("test app url: %s", testAppURL+"/health")
	_, err := utils.HTTPGetNTimes(testAppURL+"/health", numHealthChecks)
	require.NoError(t, err)

	t.Logf("invoking actor reminder scheduler")
	_, err = utils.HTTPGet(fmt.Sprintf("%s/actors/%s/abc/method/foo", testAppURL, actorTypeScheduler))
	require.NoError(t, err)

	t.Logf("registering actor reminders")
	reminder := &actorReminderRequest{
		DueTime: ptr.Of(time.Now().Add(time.Second * dueTimeScheduler).Format(time.RFC3339)),
		Period:  ptr.Of("1s"),
		Ttl:     ptr.Of("5s"),
	}
	reminderB, err := json.Marshal(reminder)
	require.NoError(t, err)

	worker := func(i int) {
		_, err = utils.HTTPPost(fmt.Sprintf("%s/actors/%s/abc/reminders/myreminder%d", testAppURL, actorTypeScheduler, i), reminderB)
		require.NoError(t, err)

		if (i+1)%10000 == 0 {
			fmt.Printf("Reminders registered: %d\n", i+1)
		}
	}

	ch := make(chan int)
	for j := 0; j < 50; j++ {
		go func() {
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
	done := time.Since(now)
	t.Logf("Created %d reminders in %s (%.1fqps)", reminderCountScheduler, done, float64(reminderCountScheduler)/done.Seconds())

	require.GreaterOrEqualf(t, dueTimeScheduler*time.Second, done, "expected to create reminders %d in less than %ss", reminderCountScheduler, dueTimeScheduler)

	t.Logf("Waiting %s for reminders to trigger", (dueTimeScheduler*time.Second)-done)
	time.Sleep((dueTimeScheduler * time.Second) - done)

	t.Logf("Waiting for %d reminders to trigger", reminderCountScheduler*5)
	start := time.Now()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := utils.HTTPGet(fmt.Sprintf("%s/remindersCount", testAppURL))
		require.NoError(t, err)
		gotCount, err := strconv.Atoi(strings.TrimSpace(string(resp)))
		require.NoError(t, err)
		assert.GreaterOrEqual(c, gotCount, reminderCountScheduler*5)
	}, 100*time.Second, time.Millisecond*100)
	done = time.Since(start)
	qps := float64(reminderCountScheduler*5) / done.Seconds()
	t.Logf("Triggered %d reminders in %s (%.1fqps)", reminderCountScheduler*5, done, qps)
	assert.GreaterOrEqual(t, qps, targetSchedulerTriggerQPS)
}
