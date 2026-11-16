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

package pull

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(invalid))
}

// invalid asserts ScheduleJob rejects an unknown priority level or a
// fairness weight outside 1 to 1000, and accepts the bounds.
type invalid struct {
	scheduler *scheduler.Scheduler
}

func (i *invalid) Setup(t *testing.T) []framework.Option {
	i.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(i.scheduler),
	}
}

func (i *invalid) Run(t *testing.T, ctx context.Context) {
	i.scheduler.WaitUntilRunning(t, ctx)
	client := i.scheduler.Client(t, ctx)

	job := func(name string, level schedulerv1.PriorityLevel, weight uint32) *schedulerv1.ScheduleJobRequest {
		req := i.scheduler.JobNowPull(name, namespace, appID, actorType, name+"::0")
		req.Metadata.PriorityLevel = level.Enum()
		req.Metadata.FairnessWeight = &weight
		return req
	}

	_, err := client.ScheduleJob(ctx, job("bad-level", schedulerv1.PriorityLevel(9), 1))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = client.ScheduleJob(ctx, job("negative-level", schedulerv1.PriorityLevel(-1), 1))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = client.ScheduleJob(ctx, job("heavy", schedulerv1.PriorityLevel_PRIORITY_LEVEL_LOW, 1001))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = client.ScheduleJob(ctx, job("zero", schedulerv1.PriorityLevel_PRIORITY_LEVEL_LOW, 0))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Empty(t, i.scheduler.EtcdJobs(t, ctx), "nothing was stored")

	_, err = client.ScheduleJob(ctx, job("min", schedulerv1.PriorityLevel_PRIORITY_LEVEL_CRITICAL, 1))
	require.NoError(t, err)
	_, err = client.ScheduleJob(ctx, job("max", schedulerv1.PriorityLevel_PRIORITY_LEVEL_BACKGROUND, 1000))
	require.NoError(t, err)
}
