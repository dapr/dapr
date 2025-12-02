/*
Copyright 2025 The Dapr Authors
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

package jobs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(list))
}

type list struct {
	daprd1    *daprd.Daprd
	daprd2    *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (l *list) Setup(t *testing.T) []framework.Option {
	l.scheduler = scheduler.New(t)

	srv := app.New(t)

	l.daprd1 = daprd.New(t,
		daprd.WithSchedulerAddresses(l.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)
	l.daprd2 = daprd.New(t,
		daprd.WithSchedulerAddresses(l.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(l.scheduler, srv, l.daprd1, l.daprd2),
	}
}

func (l *list) Run(t *testing.T, ctx context.Context) {
	l.scheduler.WaitUntilRunning(t, ctx)
	l.daprd1.WaitUntilRunning(t, ctx)
	l.daprd2.WaitUntilRunning(t, ctx)

	client := l.daprd1.GRPCClient(t, ctx)

	resp, err := client.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Empty(t, resp.GetJobs())

	_, err = client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:     "test",
			Schedule: ptr.Of("@daily"),
		},
	})
	require.NoError(t, err)

	resp, err = client.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 1)

	_, err = client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:     "test2",
			Schedule: ptr.Of("@daily"),
		},
	})
	require.NoError(t, err)
	resp, err = client.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 2)

	_, err = client.DeleteJobAlpha1(ctx, &rtv1.DeleteJobRequest{
		Name: "test",
	})
	require.NoError(t, err)
	resp, err = client.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 1)

	client2 := l.daprd2.GRPCClient(t, ctx)
	_, err = client2.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:     "test2",
			Schedule: ptr.Of("@daily"),
		},
	})
	require.NoError(t, err)
	_, err = client2.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:     "test3",
			Schedule: ptr.Of("@daily"),
		},
	})
	require.NoError(t, err)
	resp, err = client2.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 2)

	resp, err = client.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 1)
}
