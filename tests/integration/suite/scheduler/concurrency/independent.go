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

package concurrency

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(independent))
}

// independent tests that per-name gates are independent of each other. Two
// named limits (alpha=1, beta=1) should allow 1 of each concurrently, for a
// total of 2 in-flight.
type independent struct {
	scheduler *scheduler.Scheduler
}

func (ind *independent) Setup(t *testing.T) []framework.Option {
	ind.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(ind.scheduler),
	}
}

func (ind *independent) Run(t *testing.T, ctx context.Context) {
	ind.scheduler.WaitUntilRunning(t, ctx)

	client := ind.scheduler.Client(t, ctx)

	// 2 alpha jobs and 2 beta jobs.
	for _, name := range []string{"alpha-1", "alpha-2"} {
		req := ind.scheduler.JobNowActor(name, "default", "myapp", "mytype", name)
		req.Metadata.ConcurrencyKey = ptr.Of("alpha")
		_, err := client.ScheduleJob(ctx, req)
		require.NoError(t, err)
	}
	for _, name := range []string{"beta-1", "beta-2"} {
		req := ind.scheduler.JobNowActor(name, "default", "myapp", "mytype", name)
		req.Metadata.ConcurrencyKey = ptr.Of("beta")
		_, err := client.ScheduleJob(ctx, req)
		require.NoError(t, err)
	}

	stream, err := client.WatchJobs(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{
			Initial: &schedulerv1.WatchJobsRequestInitial{
				AppId:      "myapp",
				Namespace:  "default",
				ActorTypes: []string{"mytype"},
				AcceptJobTypes: []schedulerv1.JobTargetType{
					schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
				},
				ConcurrencyLimits: []*schedulerv1.ConcurrencyLimit{
					{Group: "mytype", Name: ptr.Of("alpha"), MaxConcurrent: 1},
					{Group: "mytype", Name: ptr.Of("beta"), MaxConcurrent: 1},
				},
			},
		},
	}))

	// Should receive exactly 2 triggers: 1 alpha + 1 beta.
	received := make(map[string]*schedulerv1.WatchJobsResponse)
	for range 2 {
		resp, err := stream.Recv()
		require.NoError(t, err)
		received[resp.GetName()] = resp
	}

	alphaCount := 0
	betaCount := 0
	for _, resp := range received {
		switch resp.GetMetadata().GetConcurrencyKey() {
		case "alpha":
			alphaCount++
		case "beta":
			betaCount++
		}
	}
	assert.Equal(t, 1, alphaCount, "expected 1 alpha in-flight")
	assert.Equal(t, 1, betaCount, "expected 1 beta in-flight")

	// No 3rd trigger should arrive.
	noMoreCtx, noMoreCancel := context.WithTimeout(ctx, time.Second*3)
	defer noMoreCancel()
	noMoreCh := make(chan struct{}, 1)
	go func() {
		_, err := stream.Recv()
		if err == nil {
			noMoreCh <- struct{}{}
		}
	}()

	select {
	case <-noMoreCh:
		t.Fatal("received 3rd trigger - gates should be independent")
	case <-noMoreCtx.Done():
	}

	// Ack the alpha trigger. alpha-2 should arrive but beta-2 should not.
	for name, resp := range received {
		if resp.GetMetadata().GetConcurrencyKey() == "alpha" {
			require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
					Result: &schedulerv1.WatchJobsRequestResult{
						Id:     resp.GetId(),
						Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
					},
				},
			}))
			delete(received, name)
			break
		}
	}

	select {
	case <-noMoreCh:
		// Got alpha-2.
	case <-time.After(time.Second * 10):
		t.Fatal("timed out waiting for alpha-2 after ack")
	}
}
