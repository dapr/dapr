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

package constant

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
	suite.Register(new(maxretires))
}

type maxretires struct {
	scheduler *scheduler.Scheduler
}

func (m *maxretires) Setup(t *testing.T) []framework.Option {
	m.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(m.scheduler),
	}
}

func (m *maxretires) Run(t *testing.T, ctx context.Context) {
	m.scheduler.WaitUntilRunning(t, ctx)

	client := m.scheduler.Client(t, ctx)

	job := m.scheduler.JobNowJob("test", "namespace", "appid1")
	job.Job.FailurePolicy = &schedulerv1.FailurePolicy{
		Policy: &schedulerv1.FailurePolicy_Constant{
			Constant: &schedulerv1.FailurePolicyConstant{
				Interval:   nil,
				MaxRetries: ptr.Of(uint32(4)),
			},
		},
	}

	_, err := client.ScheduleJob(ctx, job)
	require.NoError(t, err)

	triggered := m.scheduler.WatchJobsFailed(t, ctx, &schedulerv1.WatchJobsRequestInitial{
		AppId: "appid1", Namespace: "namespace",
	})

	for range 5 {
		select {
		case name := <-triggered:
			assert.Equal(t, "test", name)
		case <-time.After(time.Second * 5):
			require.Fail(t, "timed out waiting for job")
		}
	}

	select {
	case <-triggered:
		assert.Fail(t, "unexpected trigger")
	case <-time.After(time.Second * 2):
	}
}
