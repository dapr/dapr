/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(standardizedErrors))
}

type standardizedErrors struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (e *standardizedErrors) Setup(t *testing.T) []framework.Option {
	e.scheduler = scheduler.New(t)

	e.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(e.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(e.scheduler, e.daprd),
	}
}

func (e *standardizedErrors) Run(t *testing.T, ctx context.Context) {
	e.scheduler.WaitUntilRunning(t, ctx)
	e.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, e.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Covers apierrors.Empty() job is empty
	t.Run("job is empty", func(t *testing.T) {
		req := &rtv1.ScheduleJobRequest{Job: nil}

		_, err := client.ScheduleJob(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, s.Code())
		require.Equal(t, "job is empty", s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, apierrors.CodePrefixScheduler+apierrors.PostFixEmpty, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	// Covers apierrors.Empty() job name is empty
	t.Run("job name is empty", func(t *testing.T) {
		req := &rtv1.ScheduleJobRequest{Job: &rtv1.Job{Name: ""}}

		_, err := client.ScheduleJob(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, s.Code())
		require.Equal(t, "job name is empty", s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, apierrors.CodePrefixScheduler+apierrors.InFixJob+apierrors.InFixName+apierrors.PostFixEmpty, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	// Covers apierrors.Empty() job schedule is empty
	t.Run("job schedule is empty", func(t *testing.T) {
		req := &rtv1.ScheduleJobRequest{Job: &rtv1.Job{Name: "test", Schedule: ""}}

		_, err := client.ScheduleJob(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, s.Code())
		require.Equal(t, "job schedule is empty", s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, apierrors.CodePrefixScheduler+apierrors.InFixSchedule+apierrors.PostFixEmpty, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	// Covers apierrors.IncorrectNegative() job repeats negative
	t.Run("job repeats negative", func(t *testing.T) {
		req := &rtv1.ScheduleJobRequest{Job: &rtv1.Job{Name: "test", Schedule: "@daily", Repeats: -1}}

		_, err := client.ScheduleJob(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, s.Code())
		require.Equal(t, "job repeats cannot be negative", s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, apierrors.CodePrefixScheduler+apierrors.InFixNegative+apierrors.PostFixRepeats, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})
}
