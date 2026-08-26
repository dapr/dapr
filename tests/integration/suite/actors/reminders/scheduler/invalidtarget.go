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

package scheduler

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "github.com/dapr/dapr/pkg/proto/common/v1"
	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(invalidtarget))
}

type invalidtarget struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler
	daprd     *daprd.Daprd

	validCalled     atomic.Int64
	malformedCalled atomic.Int64

	loglineRejected *logline.LogLine
}

func (i *invalidtarget) Setup(t *testing.T) []framework.Option {
	i.loglineRejected = logline.New(t, logline.WithStdoutLineContains(
		"with invalid actor target metadata, rejecting",
	))

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/foo", func(http.ResponseWriter, *http.Request) {})
	handler.HandleFunc("/actors/myactortype/myactorid/method/remind/valid", func(_ http.ResponseWriter, r *http.Request) {
		i.validCalled.Add(1)
	})
	handler.HandleFunc("/", func(_ http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "malformed") {
			i.malformedCalled.Add(1)
		}
	})

	i.scheduler = scheduler.New(t)
	i.place = placement.New(t)
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	i.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(i.place.Address()),
		daprd.WithSchedulerAddresses(i.scheduler.Address()),
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithExecOptions(
			exec.WithStdout(i.loglineRejected.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(i.loglineRejected, i.scheduler, i.place, srv, i.daprd),
	}
}

func (i *invalidtarget) Run(t *testing.T, ctx context.Context) {
	i.scheduler.WaitUntilRunning(t, ctx)
	i.place.WaitUntilRunning(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	daprdURL := "http://" + i.daprd.HTTPAddress() + "/v1.0/actors/myactortype/myactorid/method/foo"
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, daprdURL, nil)
		if !assert.NoError(c, rErr) {
			return
		}
		resp, rErr := httpClient.Do(req)
		if assert.NoError(c, rErr) {
			assert.NoError(c, resp.Body.Close())
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*10, time.Millisecond*10, "actor not ready in time")

	sched := i.scheduler.Client(t, ctx)

	_, err := sched.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "no-target",
		Job:  &schedulerv1.Job{DueTime: new(time.Now().Format(time.RFC3339))},
		Metadata: &schedulerv1.JobMetadata{
			Namespace: "default", AppId: i.daprd.AppID(),
		},
	})
	require.ErrorContains(t, err, "unknown job type")

	_, err = sched.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "nil-target-type",
		Job:  &schedulerv1.Job{DueTime: new(time.Now().Format(time.RFC3339))},
		Metadata: &schedulerv1.JobMetadata{
			Namespace: "default", AppId: i.daprd.AppID(),
			Target: &schedulerv1.JobTargetMetadata{},
		},
	})
	require.ErrorContains(t, err, "unknown job type")

	_, err = sched.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "malformed",
		Job: &schedulerv1.Job{
			DueTime: new(time.Now().Format(time.RFC3339)),
			FailurePolicy: &corev1.JobFailurePolicy{
				Policy: &corev1.JobFailurePolicy_Drop{Drop: new(corev1.JobFailurePolicyDrop)},
			},
		},
		Metadata: &schedulerv1.JobMetadata{
			Namespace: "default", AppId: i.daprd.AppID(),
			Target: &schedulerv1.JobTargetMetadata{
				Type: &schedulerv1.JobTargetMetadata_Actor{
					Actor: &schedulerv1.TargetActorReminder{
						Type: "myactortype", Id: "",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	i.loglineRejected.EventuallyFoundAll(t)

	// Both daprd and the scheduler must have survived the malformed job: a
	// valid reminder scheduled afterwards is delivered end-to-end.
	_, err = sched.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "valid",
		Job:  &schedulerv1.Job{DueTime: new(time.Now().Format(time.RFC3339))},
		Metadata: &schedulerv1.JobMetadata{
			Namespace: "default", AppId: i.daprd.AppID(),
			Target: &schedulerv1.JobTargetMetadata{
				Type: &schedulerv1.JobTargetMetadata_Actor{
					Actor: &schedulerv1.TargetActorReminder{
						Type: "myactortype", Id: "myactorid",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		return i.validCalled.Load() == 1
	}, time.Second*10, time.Millisecond*10)

	assert.Zero(t, i.malformedCalled.Load())
}
