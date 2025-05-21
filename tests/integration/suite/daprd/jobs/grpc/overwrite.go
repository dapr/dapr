/*
Copyright 2022 The Dapr Authors
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

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(overwrite))
}

type overwrite struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (o *overwrite) Setup(t *testing.T) []framework.Option {
	o.scheduler = scheduler.New(t)

	o.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(o.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(o.scheduler, o.daprd),
	}
}

func (o *overwrite) Run(t *testing.T, ctx context.Context) {
	o.scheduler.WaitUntilRunning(t, ctx)
	o.daprd.WaitUntilRunning(t, ctx)

	client := o.daprd.GRPCClient(t, ctx)

	t.Run("overwrite if exists", func(t *testing.T) {
		jobName := "overwrite1"

		_, err := client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{Job: &rtv1.Job{
			Name:     jobName,
			Schedule: ptr.Of("@daily"),
			Repeats:  ptr.Of(uint32(1)),
		}})
		require.NoError(t, err)

		job, err := client.GetJobAlpha1(ctx, &rtv1.GetJobRequest{Name: jobName})
		require.Equal(t, "@daily", *job.GetJob().Schedule)
		require.NoError(t, err)

		_, err = client.ScheduleJobAlpha1(ctx, &rtv1.ScheduleJobRequest{Job: &rtv1.Job{
			Name:      jobName,
			Schedule:  ptr.Of("@weekly"),
			Repeats:   ptr.Of(uint32(1)),
			Overwrite: true,
		}})
		require.NoError(t, err)

		modifiedJob, err := client.GetJobAlpha1(ctx, &rtv1.GetJobRequest{Name: jobName})
		require.NotEqual(t, job.GetJob().GetSchedule(), modifiedJob.GetJob().GetSchedule())
		require.Equal(t, "@weekly", modifiedJob.GetJob().GetSchedule())
	})

	t.Run("do not overwrite if exists", func(t *testing.T) {
		r := &rtv1.ScheduleJobRequest{Job: &rtv1.Job{
			Name:     "overwrite2",
			Schedule: ptr.Of("@daily"),
			Repeats:  ptr.Of(uint32(1)),
		}}
		_, err := client.ScheduleJobAlpha1(ctx, r)
		require.NoError(t, err)

		for _, req := range []*rtv1.ScheduleJobRequest{
			{Job: &rtv1.Job{
				Name:      "overwrite2",
				Schedule:  ptr.Of("@daily"),
				Repeats:   ptr.Of(uint32(1)),
				Overwrite: false,
			}},
		} {
			_, err := client.ScheduleJobAlpha1(ctx, req)
			require.Error(t, err)
			require.Equal(t, codes.AlreadyExists, status.Code(err))
		}
	})
}
