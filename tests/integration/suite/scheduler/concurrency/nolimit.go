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
)

func init() {
	suite.Register(new(nolimit))
}

// nolimit tests that when no concurrency limits are configured in the initial
// request, all jobs are dispatched without throttling.
type nolimit struct {
	scheduler *scheduler.Scheduler
}

func (n *nolimit) Setup(t *testing.T) []framework.Option {
	n.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(n.scheduler),
	}
}

func (n *nolimit) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)

	client := n.scheduler.Client(t, ctx)

	// Schedule 10 jobs.
	for i := range 10 {
		_, err := client.ScheduleJob(ctx, n.scheduler.JobNowActor(
			"job-"+string(rune('a'+i)), "default", "myapp", "mytype", "id-"+string(rune('a'+i)),
		))
		require.NoError(t, err)
	}

	// Open stream with NO concurrency limits.
	triggered := n.scheduler.WatchJobsSuccess(t, ctx, &schedulerv1.WatchJobsRequestInitial{
		AppId:      "myapp",
		Namespace:  "default",
		ActorTypes: []string{"mytype"},
		AcceptJobTypes: []schedulerv1.JobTargetType{
			schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
		},
	})

	// All 10 should be dispatched without any throttling.
	received := 0
	deadline := time.After(time.Second * 10)
	for received < 10 {
		select {
		case <-triggered:
			received++
		case <-deadline:
			t.Fatalf("timed out, only received %d/10 triggers", received)
		}
	}

	assert.Equal(t, 10, received)
}
