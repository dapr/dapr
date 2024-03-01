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
	getJobFn      func(context.Context, *schedulerv1pb.JobRequest) (*schedulerv1pb.GetJobResponse, error)
	listJobFn     func(context.Context, *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error)
	deleteJobFn   func(context.Context, *schedulerv1pb.JobRequest) (*schedulerv1pb.DeleteJobResponse, error)
	connectHostFn func(context.Context, *schedulerv1pb.ConnectHostRequest) (*schedulerv1pb.ConnectHostResponse, error)
}

func (s *server) ScheduleJob(ctx context.Context, request *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	if s.scheduleJobFn != nil {
		return s.scheduleJobFn(ctx, request)
	}
	return nil, nil
}

func (s *server) GetJob(ctx context.Context, request *schedulerv1pb.JobRequest) (*schedulerv1pb.GetJobResponse, error) {
	if s.getJobFn != nil {
		return s.getJobFn(ctx, request)
	}
	return nil, nil
}

func (s *server) ListJobs(ctx context.Context, request *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
	if s.listJobFn != nil {
		return s.listJobFn(ctx, request)
	}
	return nil, nil
}

func (s *server) DeleteJob(ctx context.Context, request *schedulerv1pb.JobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	if s.deleteJobFn != nil {
		return s.deleteJobFn(ctx, request)
	}
	return nil, nil
}

func (s *server) ConnectHost(ctx context.Context, request *schedulerv1pb.ConnectHostRequest) (*schedulerv1pb.ConnectHostResponse, error) {
	if s.connectHostFn != nil {
		return s.connectHostFn(ctx, request)
	}
	return nil, nil
}
