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

package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(overrideroute))
}

// overrideroute asserts that a job scheduled with an override route path is
// delivered to the app on `job/{override_route_path}` rather than
// `job/{name}`, that the override route path is returned when getting and
// listing jobs, and that invalid override route paths are rejected.
type overrideroute struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
	jobChan   chan *runtimev1pb.JobEventRequest
}

func (o *overrideroute) Setup(t *testing.T) []framework.Option {
	o.scheduler = scheduler.New(t)

	o.jobChan = make(chan *runtimev1pb.JobEventRequest, 1)
	srv := app.New(t,
		app.WithOnJobEventFn(func(ctx context.Context, in *runtimev1pb.JobEventRequest) (*runtimev1pb.JobEventResponse, error) {
			o.jobChan <- in
			return new(runtimev1pb.JobEventResponse), nil
		}),
	)

	o.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(o.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(o.scheduler, srv, o.daprd),
	}
}

func (o *overrideroute) Run(t *testing.T, ctx context.Context) {
	o.scheduler.WaitUntilRunning(t, ctx)
	o.daprd.WaitUntilRunning(t, ctx)

	client := o.daprd.GRPCClient(t, ctx)

	t.Run("job is delivered on the override route path", func(t *testing.T) {
		_, err := client.ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:              "sync-video-state-123",
				DueTime:           new("0s"),
				OverrideRoutePath: new("sync-video-state/123"),
			},
		})
		require.NoError(t, err)

		select {
		case job := <-o.jobChan:
			assert.Equal(t, "sync-video-state-123", job.GetName())
			assert.Equal(t, "job/sync-video-state/123", job.GetMethod())
		case <-time.After(time.Second * 10):
			require.Fail(t, "timed out waiting for triggered job")
		}
	})

	t.Run("job without override route path is delivered on its name", func(t *testing.T) {
		_, err := client.ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:    "no-override",
				DueTime: new("0s"),
			},
		})
		require.NoError(t, err)

		select {
		case job := <-o.jobChan:
			assert.Equal(t, "no-override", job.GetName())
			assert.Equal(t, "job/no-override", job.GetMethod())
		case <-time.After(time.Second * 10):
			require.Fail(t, "timed out waiting for triggered job")
		}
	})

	t.Run("override route path is returned by get and list", func(t *testing.T) {
		_, err := client.ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:              "with-override",
				Schedule:          new("@daily"),
				OverrideRoutePath: new("my/route"),
			},
		})
		require.NoError(t, err)
		_, err = client.ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:     "without-override",
				Schedule: new("@daily"),
			},
		})
		require.NoError(t, err)

		resp, err := client.GetJob(ctx, &runtimev1pb.GetJobRequest{Name: "with-override"})
		require.NoError(t, err)
		require.NotNil(t, resp.GetJob().OverrideRoutePath)
		assert.Equal(t, "my/route", resp.GetJob().GetOverrideRoutePath())

		resp, err = client.GetJob(ctx, &runtimev1pb.GetJobRequest{Name: "without-override"})
		require.NoError(t, err)
		assert.Nil(t, resp.GetJob().OverrideRoutePath)

		list, err := client.ListJobs(ctx, new(runtimev1pb.ListJobsRequest))
		require.NoError(t, err)
		routes := make(map[string]*string)
		for _, job := range list.GetJobs() {
			routes[job.GetName()] = job.OverrideRoutePath
		}
		require.Contains(t, routes, "with-override")
		require.Contains(t, routes, "without-override")
		require.NotNil(t, routes["with-override"])
		assert.Equal(t, "my/route", *routes["with-override"])
		assert.Nil(t, routes["without-override"])
	})

	t.Run("invalid override route paths are rejected", func(t *testing.T) {
		for _, path := range []string{
			"",
			"/leading-slash",
			"trailing-slash/",
			"double//slash",
			"../escape",
			"a/./b",
			"query?a=b",
			"fragment#a",
			"percent%2Fencoded",
			"white space",
		} {
			t.Run(path, func(t *testing.T) {
				_, err := client.ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
					Job: &runtimev1pb.Job{
						Name:              "invalid",
						Schedule:          new("@daily"),
						OverrideRoutePath: new(path),
					},
				})
				require.Error(t, err)

				s, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, codes.InvalidArgument, s.Code())

				require.Len(t, s.Details(), 1)
				errInfo, ok := s.Details()[0].(*errdetails.ErrorInfo)
				require.True(t, ok)
				assert.Equal(t, "DAPR_SCHEDULER_JOB_OVERRIDE_ROUTE_PATH", errInfo.GetReason())
				assert.Equal(t, "dapr.io", errInfo.GetDomain())
			})
		}

		_, err := client.GetJob(ctx, &runtimev1pb.GetJobRequest{Name: "invalid"})
		require.Error(t, err)
	})
}
