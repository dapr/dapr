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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(multiapp))
}

// multiapp tests that concurrency gates from different apps in the same
// namespace are independent. App A's gate should not be removed when app B
// connects without limits, and app B's triggers should not be throttled by app
// A's limits.
type multiapp struct {
	scheduler *scheduler.Scheduler
}

func (m *multiapp) Setup(t *testing.T) []framework.Option {
	m.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(m.scheduler),
	}
}

func (m *multiapp) Run(t *testing.T, ctx context.Context) {
	m.scheduler.WaitUntilRunning(t, ctx)

	client := m.scheduler.Client(t, ctx)

	// Schedule jobs for both apps.
	for _, name := range []string{"appA-1", "appA-2", "appA-3"} {
		_, err := client.ScheduleJob(ctx, m.scheduler.JobNowActor(
			name, "default", "appA", "typeA", name,
		))
		require.NoError(t, err)
	}
	for _, name := range []string{"appB-1", "appB-2", "appB-3"} {
		_, err := client.ScheduleJob(ctx, m.scheduler.JobNowActor(
			name, "default", "appB", "typeB", name,
		))
		require.NoError(t, err)
	}

	// App A connects with a limit of 1.
	var appADispatched atomic.Int64
	streamA, err := client.WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, streamA.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{
			Initial: &schedulerv1.WatchJobsRequestInitial{
				AppId:      "appA",
				Namespace:  "default",
				ActorTypes: []string{"typeA"},
				AcceptJobTypes: []schedulerv1.JobTargetType{
					schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
				},
				ConcurrencyLimits: []*schedulerv1.ConcurrencyLimit{
					{Group: "typeA", MaxConcurrent: 1},
				},
			},
		},
	}))
	go func() {
		for {
			if _, serr := streamA.Recv(); serr != nil {
				return
			}
			appADispatched.Add(1)
		}
	}()

	// App B connects with NO limits. This should not remove app A's gate.
	var appBDispatched atomic.Int64
	streamB, err := client.WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, streamB.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{
			Initial: &schedulerv1.WatchJobsRequestInitial{
				AppId:      "appB",
				Namespace:  "default",
				ActorTypes: []string{"typeB"},
				AcceptJobTypes: []schedulerv1.JobTargetType{
					schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
				},
			},
		},
	}))
	go func() {
		for {
			if _, err := streamB.Recv(); err != nil {
				return
			}
			appBDispatched.Add(1)
		}
	}()

	// App B should get all 3 triggers (no limits).
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(3), appBDispatched.Load())
	}, time.Second*10, time.Millisecond*100)

	// App A should only get 1 trigger (limit=1, no acks sent).
	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(1), appADispatched.Load(),
		"app A should be throttled to 1, app B's connection should not have removed app A's gate")
}
