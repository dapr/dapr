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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(multiScheduler))
}

// multiScheduler tests that concurrency limits are enforced across a 3-node
// scheduler cluster. With a global limit of 6 and 3 schedulers, each gets a
// local limit of 2. Clients connect to each scheduler and verify the combined
// throughput respects the global limit.
type multiScheduler struct {
	cluster *cluster.Cluster
}

func (m *multiScheduler) Setup(t *testing.T) []framework.Option {
	m.cluster = cluster.New(t, cluster.WithCount(3))
	return []framework.Option{
		framework.WithProcesses(m.cluster),
	}
}

func (m *multiScheduler) Run(t *testing.T, ctx context.Context) {
	m.cluster.WaitUntilRunning(t, ctx)

	concurrencyLimit := &schedulerv1.ConcurrencyLimit{
		Group: "mytype", MaxConcurrent: 6,
	}
	initial := &schedulerv1.WatchJobsRequestInitial{
		AppId:      "myapp",
		Namespace:  "default",
		ActorTypes: []string{"mytype"},
		AcceptJobTypes: []schedulerv1.JobTargetType{
			schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
		},
		ConcurrencyLimits: []*schedulerv1.ConcurrencyLimit{concurrencyLimit},
	}

	// Open a watch stream on each scheduler. Each gets localLimit = 6/3 = 2.
	var totalDispatched atomic.Int64
	streams := make([]schedulerv1.Scheduler_WatchJobsClient, 3)
	for i := range 3 {
		client := m.cluster.ClientN(t, ctx, i)
		stream, err := client.WatchJobs(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{Initial: initial},
		}))
		streams[i] = stream

		go func(s schedulerv1.Scheduler_WatchJobsClient) {
			for {
				if _, err := s.Recv(); err != nil {
					return
				}
				totalDispatched.Add(1)
			}
		}(stream)
	}

	// Schedule 12 jobs across all 3 schedulers.
	for i := range 12 {
		client := m.cluster.ClientN(t, ctx, i%3)
		_, err := client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
			Name: "job-" + string(rune('a'+i)),
			Job:  &schedulerv1.Job{DueTime: new(time.Now().Format(time.RFC3339))},
			Metadata: &schedulerv1.JobMetadata{
				AppId: "myapp", Namespace: "default",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{
							Type: "mytype", Id: "id-" + string(rune('a'+i)),
						},
					},
				},
			},
		})
		require.NoError(t, err)
	}

	// Each scheduler has localLimit=2, so total dispatched should reach exactly 6.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(6), totalDispatched.Load())
	}, time.Second*10, time.Millisecond*100)

	// Verify the limit holds - no more than 6 dispatched without acks.
	time.Sleep(time.Second * 3)
	assert.Equal(t, int64(6), totalDispatched.Load())
}
