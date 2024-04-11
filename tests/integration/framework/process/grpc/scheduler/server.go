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

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type server struct {
	scheduleJobFn func(ctx context.Context, request *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error)
	getJobFn      func(context.Context, *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error)
	deleteJobFn   func(context.Context, *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error)
	watchJobsFn   func(*schedulerv1pb.WatchJobsRequest, schedulerv1pb.Scheduler_WatchJobsServer) error
}

func (s *server) ScheduleJob(ctx context.Context, request *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	if s.scheduleJobFn != nil {
		return s.scheduleJobFn(ctx, request)
	}
	return nil, nil
}

func (s *server) GetJob(ctx context.Context, request *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error) {
	if s.getJobFn != nil {
		return s.getJobFn(ctx, request)
	}
	return nil, nil
}

func (s *server) DeleteJob(ctx context.Context, request *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	if s.deleteJobFn != nil {
		return s.deleteJobFn(ctx, request)
	}
	return nil, nil
}

func (s *server) WatchJobs(request *schedulerv1pb.WatchJobsRequest, srv schedulerv1pb.Scheduler_WatchJobsServer) error {
	if s.watchJobsFn != nil {
		return s.watchJobsFn(request, srv)
	}
	return nil
}
