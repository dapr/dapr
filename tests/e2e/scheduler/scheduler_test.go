//go:build e2e
// +build e2e

/*
Copyright 2024 The Dapr Authors
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

package scheduler_e2e

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

const (
	appPortGRPC               = 3001
	appNameGRPC               = "schedulerapp-grpc"
	appName                   = "schedulerapp"
	numHealthChecks           = 10                                     // Number of get calls before starting tests.
	numIterations             = 4                                      // Number of times each test should run.
	jobName                   = "testjob"                              // Job name.
	scheduleJobURLFormat      = "%s/scheduleJob/" + jobName + "-%s-%s" // App Schedule Job URL.
	getJobURLFormat           = "%s/getJob/" + jobName + "-%s-%s"      // App Schedule Job URL.
	deleteJobURLFormat        = "%s/deleteJob/" + jobName + "-%s-%s"   // App Schedule Job URL.
	getTriggeredJobsURLFormat = "%s/getTriggeredJobs"                  // App Get the Triggered Jobs URL.
	numJobsPerGoRoutine       = 10                                     // Number of get calls before starting tests.
)

type triggeredJob struct {
	TypeURL string `json:"type_url"`
	Value   string `json:"value"`
}

type jobData struct {
	DataType string `json:"@type"`
	Value    string `json:"value"`
}

type job struct {
	Data     jobData `json:"data,omitempty"`
	Schedule string  `json:"schedule,omitempty"`
	Repeats  int     `json:"repeats,omitempty"`
	DueTime  string  `json:"dueTime,omitempty"`
}

type receivedJob struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Repeats  int    `json:"repeats"`
	DueTime  string `json:"dueTime"`
	Data     struct {
		Type  string `json:"@type"`
		Value struct {
			Type  string `json:"@type"`
			Value string `json:"value"`
		} `json:"value"`
	} `json:"data"`
}

var tr *runner.TestRunner

func TestMain(m *testing.M) {
	utils.SetupLogs("scheduler")
	utils.InitHTTPClient(true)

	testApps := []kube.AppDescription{
		// HTTP test
		{
			AppName:             appName,
			DaprEnabled:         true,
			DebugLoggingEnabled: true,
			ImageName:           "e2e-schedulerapp",
			Replicas:            1,
			IngressEnabled:      true,
			MetricsEnabled:      true,
		},
		// GRPC test
		{
			AppName:             appNameGRPC,
			DaprEnabled:         true,
			DebugLoggingEnabled: true,
			ImageName:           "e2e-schedulerapp_grpc",
			Replicas:            1,
			IngressEnabled:      true,
			MetricsEnabled:      true,
			AppProtocol:         "grpc",
			AppPort:             appPortGRPC,
		},
	}

	tr = runner.NewTestRunner(appName, testApps, nil, nil)
	os.Exit(tr.Start(m))
}

// This func will test that jobs are able to be scheduled and triggered properly along with
// doing a basic schedule, get, delete test to ensure that the Scheduler is operating properly.
func TestJobs(t *testing.T) {
	externalURL := tr.Platform.AcquireAppExternalURL(appName)

	require.NotEmpty(t, externalURL, "external URL must not be empty!")

	t.Logf("Checking if app is healthy ...")
	_, err := utils.HTTPGetNTimes(externalURL, numHealthChecks)
	require.NoError(t, err)

	t.Run("Schedule job and app should receive triggered job.", func(t *testing.T) {
		data := jobData{
			DataType: "type.googleapis.com/google.protobuf.StringValue",
			Value:    "expression",
		}

		j := job{
			Data:     data,
			Schedule: "@every 1s",
			Repeats:  1,
			DueTime:  "1s",
		}
		jobBody, err := json.Marshal(j)
		require.NoError(t, err)

		var wg sync.WaitGroup
		for iteration := 1; iteration <= numIterations; iteration++ {
			wg.Add(1)
			go func(iteration int) {
				defer wg.Done()
				t.Logf("Running iteration %d out of %d ...", iteration, numIterations)

				for i := 0; i < numJobsPerGoRoutine; i++ {
					// Call app to schedule job, send job to app
					log.Printf("Scheduling job: testjob-%s-%s", strconv.Itoa(iteration), strconv.Itoa(i))
					_, err := utils.HTTPPost(fmt.Sprintf(scheduleJobURLFormat, externalURL, strconv.Itoa(iteration), strconv.Itoa(i)), jobBody)
					require.NoError(t, err)
				}
			}(iteration)
		}
		wg.Wait()

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			log.Println("Checking the count of stored triggered jobs equals the scheduled count of jobs")
			// Call the app endpoint to get triggered jobs
			resp, err := utils.HTTPGet(fmt.Sprintf(getTriggeredJobsURLFormat, externalURL))

			require.NoError(t, err)
			var triggeredJobs []triggeredJob
			err = json.Unmarshal([]byte(resp), &triggeredJobs)
			require.NoError(t, err)

			// Check if the length of triggeredJobs matches the expected length of scheduled jobs
			assert.Len(c, triggeredJobs, numIterations*numJobsPerGoRoutine)
		}, 10*time.Second, 100*time.Millisecond)
		t.Log("Done.")
	})

	// create, get, delete jobs
	t.Run("Schedule, get, delete jobs.", func(t *testing.T) {
		data := jobData{
			DataType: "type.googleapis.com/google.protobuf.StringValue",
			Value:    "expression",
		}

		scheduledJob := job{
			Data:     data,
			Schedule: "@every 60s",
			Repeats:  1,
			DueTime:  "180s", // higher due time bc we already test triggering above

		}
		jobBody, err := json.Marshal(scheduledJob)
		require.NoError(t, err)

		var wg sync.WaitGroup
		for iteration := 1; iteration <= numIterations; iteration++ {
			wg.Add(1)
			go func(iteration int) {
				defer wg.Done()
				t.Logf("(Schedule job) Running iteration %d out of %d ...", iteration, numIterations)

				for i := 0; i < numJobsPerGoRoutine; i++ {
					// Call app to schedule job, send job to app
					log.Printf("Scheduling job: testjob-%s-%s", strconv.Itoa(iteration), strconv.Itoa(i))
					_, err := utils.HTTPPost(fmt.Sprintf(scheduleJobURLFormat, externalURL, strconv.Itoa(iteration), strconv.Itoa(i)), jobBody)
					require.NoError(t, err)
				}
			}(iteration)
		}
		wg.Wait()

		// get all above jobs
		var getWg sync.WaitGroup
		for iteration := 1; iteration <= numIterations; iteration++ {
			getWg.Add(1)
			go func(iteration int) {
				defer getWg.Done()
				t.Logf("(Get job) Running iteration %d out of %d ...", iteration, numIterations)

				for i := 0; i < numJobsPerGoRoutine; i++ {
					// Call app to get job, send request to app which makes the dapr call
					log.Printf("Getting job: testjob-%s-%s", strconv.Itoa(iteration), strconv.Itoa(i))
					resp, err := utils.HTTPGet(fmt.Sprintf(getJobURLFormat, externalURL, strconv.Itoa(iteration), strconv.Itoa(i)))
					require.NoError(t, err)
					assert.NotNil(t, resp)

					var rJob receivedJob
					err = json.Unmarshal(resp, &rJob)
					require.NoError(t, err)

					assert.Equal(t, jobName+"-"+strconv.Itoa(iteration)+"-"+strconv.Itoa(i), rJob.Name)
					assert.Equal(t, scheduledJob.Schedule, rJob.Schedule)
					assert.Equal(t, scheduledJob.Repeats, rJob.Repeats)
					assert.Equal(t, scheduledJob.DueTime, rJob.DueTime)
					assert.Equal(t, scheduledJob.Data.Value, rJob.Data.Value.Value)
				}
			}(iteration)
		}
		getWg.Wait()

		// delete all above jobs
		var deleteWg sync.WaitGroup
		for iteration := 1; iteration <= numIterations; iteration++ {
			deleteWg.Add(1)
			go func(iteration int) {
				defer deleteWg.Done()
				// Call app to delete job, send request to app which makes the dapr call
				t.Logf("(Delete job) Running iteration %d out of %d ...", iteration, numIterations)
				for i := 0; i < numJobsPerGoRoutine; i++ {
					log.Printf("Deleting job: testjob-%s-%s", strconv.Itoa(iteration), strconv.Itoa(i))
					_, err := utils.HTTPDelete(fmt.Sprintf(deleteJobURLFormat, externalURL, strconv.Itoa(iteration), strconv.Itoa(i)))
					require.NoError(t, err)
				}
			}(iteration)
		}
		deleteWg.Wait()
		t.Log("Done.")
	})
}
