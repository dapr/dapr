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

package api

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd     *daprd.Daprd
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	e.sentry = sentry.New(t)

	e.scheduler = scheduler.New(t,
		scheduler.WithSentry(e.sentry),
		scheduler.WithScheduleJobFn(
			func(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
				return nil, stderrors.New("schedule job error")
			}),
		scheduler.WithGetJobFn(
			func(ctx context.Context, req *schedulerv1pb.JobRequest) (*schedulerv1pb.GetJobResponse, error) {
				return nil, stderrors.New("get job error")
			}),
		scheduler.WithListJobsFn(func(ctx context.Context, request *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
			return nil, stderrors.New("list jobs error")
		}),
		scheduler.WithDeleteJobFn(func(ctx context.Context, request *schedulerv1pb.JobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
			return nil, stderrors.New("delete job error")
		}),
	)

	e.daprd = daprd.New(t,
		daprd.WithSchedulerAddress(e.scheduler.Address(t)),
		daprd.WithSentryAddress(e.sentry.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(e.sentry.CABundle().TrustAnchors)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(e.sentry, e.scheduler, e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.sentry.WaitUntilRunning(t, ctx)
	e.daprd.WaitUntilRunning(t, ctx)

	client := e.daprd.GRPCClient(t, ctx)

	// Covers errors returned from the scheduler server and caught in universal
	t.Run("schedule job", func(t *testing.T) {
		req := &rtv1.ScheduleJobRequest{Job: &rtv1.Job{Name: "test", Schedule: "@daily"}}

		_, err := client.ScheduleJob(ctx, req)
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Internal, s.Code())
		require.Contains(t, s.Message(), apierrors.MsgScheduleJob)

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixSchedule, apierrors.PostFixJob), errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	// Covers errors returned from the scheduler server and caught in universal
	t.Run("get job", func(t *testing.T) {
		req := &rtv1.GetJobRequest{Name: "test"}
		_, err := client.GetJob(ctx, req)
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Internal, s.Code())
		require.Contains(t, s.Message(), apierrors.MsgGetJob)

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixGet, apierrors.PostFixJob), errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	// Covers errors returned from the scheduler server and caught in universal
	t.Run("list jobs", func(t *testing.T) {
		req := &rtv1.ListJobsRequest{AppId: "test"}

		_, err := client.ListJobs(ctx, req)
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Internal, s.Code())
		require.Contains(t, s.Message(), apierrors.MsgListJobs)

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixList, apierrors.PostFixJobs), errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	// Covers errors returned from the scheduler server and caught in universal
	t.Run("delete job", func(t *testing.T) {
		req := &rtv1.DeleteJobRequest{Name: "test"}

		_, err := client.DeleteJob(ctx, req)
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Internal, s.Code())
		require.Contains(t, s.Message(), apierrors.MsgDeleteJob)

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixDelete, apierrors.PostFixJob), errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})
}
