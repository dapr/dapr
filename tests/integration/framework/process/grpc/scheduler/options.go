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

package scheduler

import (
	"context"

	"google.golang.org/grpc"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
)

// options contains the options for running the Scheduler in integration tests.
type options struct {
	grpcopts []procgrpc.Option
	sentry   *sentry.Sentry

	withRegister func(*grpc.Server)

	scheduleJobFn func(context.Context, *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error)
	getJobFn      func(context.Context, *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error)
	deleteJobFn   func(context.Context, *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error)
	watchJobsFn   func(schedulerv1pb.Scheduler_WatchJobsServer) error
}

func WithSentry(sentry *sentry.Sentry) func(*options) {
	return func(o *options) {
		o.sentry = sentry
	}
}

func WithGRPCOptions(opts ...procgrpc.Option) func(*options) {
	return func(o *options) {
		o.grpcopts = opts
	}
}

func WithScheduleJobFn(fn func(ctx context.Context, request *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error)) func(*options) {
	return func(o *options) {
		o.scheduleJobFn = fn
	}
}

func WithGetJobFn(fn func(ctx context.Context, request *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error)) func(*options) {
	return func(o *options) {
		o.getJobFn = fn
	}
}

func WithDeleteJobFn(fn func(ctx context.Context, request *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error)) func(*options) {
	return func(o *options) {
		o.deleteJobFn = fn
	}
}

func WithWatchJobsFn(fn func(schedulerv1pb.Scheduler_WatchJobsServer) error) func(*options) {
	return func(o *options) {
		o.watchJobsFn = fn
	}
}
