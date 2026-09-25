/*
Copyright 2026 The Dapr Authors
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

package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(deleteduerace))
}

const (
	deleteDueRaceWorkers    = 8
	deleteDueRacePerWorker  = 250
	deleteDueRaceMaxRuntime = 45 * time.Second
)

// deleteduerace verifies that deleting or overwriting jobs while their
// triggers are firing never tears down the scheduler server. A trigger that
// fires while its job is being deleted or overwritten races the job's close in
// go-etcd-cron: when the close wins, the late ExecuteRequest finds no counter,
// which must be dropped rather than failing the cron loop and recreating the
// whole server incl. embedded etcd.
type deleteduerace struct {
	scheduler *scheduler.Scheduler
	logline   *logline.LogLine
}

func (d *deleteduerace) Setup(t *testing.T) []framework.Option {
	d.logline = logline.New(t, logline.WithCaptureAll())

	// Capture both streams so the logline pipes close when the scheduler exits.
	d.scheduler = scheduler.New(t,
		scheduler.WithExecOptions(
			exec.WithStdout(d.logline.Stdout()),
			exec.WithStderr(d.logline.Stderr()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(d.logline, d.scheduler),
	}
}

func (d *deleteduerace) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)

	sched := d.scheduler.Client(t, ctx)

	metadata := &schedulerv1.JobMetadata{
		AppId:     "appid",
		Namespace: "namespace",
		Target: &schedulerv1.JobTargetMetadata{
			Type: new(schedulerv1.JobTargetMetadata_Job),
		},
	}

	// Ack every trigger straight away so jobs keep firing continuously, keeping
	// a trigger in flight whenever a delete lands.
	watchCtx, watchCancel := context.WithCancel(ctx)
	watch, err := sched.WatchJobs(watchCtx)
	require.NoError(t, err)
	require.NoError(t, watch.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{
			Initial: &schedulerv1.WatchJobsRequestInitial{
				AppId:     metadata.GetAppId(),
				Namespace: metadata.GetNamespace(),
			},
		},
	}))

	var triggered atomic.Int64
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		for {
			job, rerr := watch.Recv()
			if rerr != nil {
				return
			}
			triggered.Add(1)
			if serr := watch.Send(&schedulerv1.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
					Result: &schedulerv1.WatchJobsRequestResult{
						Id:     job.GetId(),
						Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
					},
				},
			}); serr != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		watchCancel()
		<-watchDone
	})

	// Deleting a job and overwriting it both close the job's counter in
	// go-etcd-cron (an overwrite is informed as a delete of the old revision),
	// so exercise both.
	d.churn(t, ctx, sched, metadata, false)
	d.churn(t, ctx, sched, metadata, true)

	assert.Positive(t, triggered.Load(), "jobs must have fired for the race to be exercised")
	assert.Never(t, func() bool {
		return d.logline.Contains("Scheduler server failed, recreating in")
	}, 3*time.Second, 50*time.Millisecond,
		"scheduler server must not be recreated when no fault was injected")
	assert.False(t, d.logline.Contains("counter not found for modRevision"),
		"cron worker must not fail on an ExecuteRequest for a closed job")
	assert.False(t, d.logline.Contains("cron instance shutdown"),
		"cron instance must not shut down when no fault was injected")

	for w := range deleteDueRaceWorkers {
		_, err = sched.DeleteJob(ctx, &schedulerv1.DeleteJobRequest{
			Name:     fmt.Sprintf("race-%d", w),
			Metadata: metadata,
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, d.scheduler.ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*10, 10*time.Millisecond)

	httpClient := client.HTTP(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/healthz", d.scheduler.HealthzPort()), nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// churn schedules continuously firing jobs from concurrent workers and, while
// they fire, deletes them or (if overwrite) re-schedules them under the same
// name.
func (d *deleteduerace) churn(t *testing.T, ctx context.Context, sched schedulerv1.SchedulerClient, metadata *schedulerv1.JobMetadata, overwrite bool) {
	t.Helper()

	deadline := time.Now().Add(deleteDueRaceMaxRuntime)
	errCh := make(chan error, deleteDueRaceWorkers)
	var wg sync.WaitGroup
	for w := range deleteDueRaceWorkers {
		wg.Go(func() {
			for i := range deleteDueRacePerWorker {
				if time.Now().After(deadline) || ctx.Err() != nil {
					return
				}

				name := fmt.Sprintf("race-%d-%d", w, i)
				if overwrite {
					// All iterations of a worker share one name.
					name = fmt.Sprintf("race-%d", w)
				}

				if _, err := sched.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
					Name: name,
					Job: &schedulerv1.Job{
						DueTime:  new(time.Now().Format(time.RFC3339)),
						Schedule: new("@every 1ms"),
					},
					Metadata:  metadata,
					Overwrite: overwrite,
				}); err != nil {
					errCh <- fmt.Errorf("schedule %s: %w", name, err)
					return
				}

				// Stagger the next write so it lands at varying points of the
				// job's trigger cycle. An overwritten job is replaced under the
				// same name, so give it longer to fire before replacing it.
				if overwrite {
					time.Sleep(time.Duration(5+i%20) * time.Millisecond)
					continue
				}
				time.Sleep(time.Duration(i%5) * time.Millisecond)

				if _, err := sched.DeleteJob(ctx, &schedulerv1.DeleteJobRequest{
					Name:     name,
					Metadata: metadata,
				}); err != nil {
					errCh <- fmt.Errorf("delete %s: %w", name, err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(errCh)

	// "cron is closed" or Unavailable here means the server incarnation was
	// torn down underneath the client.
	for err := range errCh {
		assert.NoError(t, err, "overwrite=%t", overwrite)
	}
}
