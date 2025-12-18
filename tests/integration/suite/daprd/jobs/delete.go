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
	"strconv"
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
	suite.Register(new(delete))
}

type delete struct {
	daprd1    *daprd.Daprd
	daprd2    *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (d *delete) Setup(t *testing.T) []framework.Option {
	d.scheduler = scheduler.New(t)

	srv := app.New(t)

	d.daprd1 = daprd.New(t,
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)
	d.daprd2 = daprd.New(t,
		daprd.WithSchedulerAddresses(d.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(d.scheduler, srv, d.daprd1, d.daprd2),
	}
}

func (d *delete) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)
	d.daprd1.WaitUntilRunning(t, ctx)
	d.daprd2.WaitUntilRunning(t, ctx)

	client1 := d.daprd1.GRPCClient(t, ctx)
	client2 := d.daprd2.GRPCClient(t, ctx)

	resp, err := client1.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Empty(t, resp.GetJobs())
	_, err = client1.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{Name: "test", Schedule: ptr.Of("@daily")},
	})
	require.NoError(t, err)
	resp, err = client1.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 1)
	resp, err = client2.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Empty(t, resp.GetJobs())

	_, err = client1.DeleteJobAlpha1(ctx, &rtv1.DeleteJobRequest{
		Name: "test",
	})
	require.NoError(t, err)
	resp, err = client1.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Empty(t, resp.GetJobs())
	resp, err = client2.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Empty(t, resp.GetJobs())

	for i := range 5 {
		_, err = client1.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
			Job: &rtv1.Job{Name: "abc-" + strconv.Itoa(i), Schedule: ptr.Of("@daily")},
		})
		require.NoError(t, err)
	}
	for i := range 5 {
		_, err = client1.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
			Job: &rtv1.Job{Name: "helloworld-" + strconv.Itoa(i), Schedule: ptr.Of("@daily")},
		})
		require.NoError(t, err)
	}
	for i := range 5 {
		_, err = client2.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{
			Job: &rtv1.Job{Name: "abc-" + strconv.Itoa(i), Schedule: ptr.Of("@daily")},
		})
		require.NoError(t, err)
	}

	resp, err = client1.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 10)
	resp, err = client2.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 5)

	_, err = client1.DeleteJobsByPrefixAlpha1(ctx, &rtv1.DeleteJobsByPrefixRequestAlpha1{
		NamePrefix: ptr.Of("abc-"),
	})
	require.NoError(t, err)
	resp, err = client1.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 5)
	resp, err = client2.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 5)

	_, err = client1.DeleteJobsByPrefixAlpha1(ctx, &rtv1.DeleteJobsByPrefixRequestAlpha1{
		NamePrefix: nil,
	})
	require.NoError(t, err)
	resp, err = client1.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Empty(t, resp.GetJobs())
	resp, err = client2.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 5)

	_, err = client2.DeleteJobsByPrefixAlpha1(ctx, &rtv1.DeleteJobsByPrefixRequestAlpha1{
		NamePrefix: ptr.Of("helloworld-"),
	})
	require.NoError(t, err)
	resp, err = client1.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Empty(t, resp.GetJobs())
	resp, err = client2.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Len(t, resp.GetJobs(), 5)

	_, err = client2.DeleteJobsByPrefixAlpha1(ctx, &rtv1.DeleteJobsByPrefixRequestAlpha1{
		NamePrefix: ptr.Of("abc-"),
	})
	require.NoError(t, err)
	resp, err = client1.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Empty(t, resp.GetJobs())
	resp, err = client2.ListJobsAlpha1(ctx, new(rtv1.ListJobsRequestAlpha1))
	require.NoError(t, err)
	assert.Empty(t, resp.GetJobs())
}
