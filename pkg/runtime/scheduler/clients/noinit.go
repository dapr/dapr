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

package clients

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type schedulerClientNotInitialized struct{}

var schedulerClientNotInitializedInstance = schedulerClientNotInitialized{}

func (c *schedulerClientNotInitialized) ScheduleJob(ctx context.Context, in *schedulerv1pb.ScheduleJobRequest, opts ...grpc.CallOption) (*schedulerv1pb.ScheduleJobResponse, error) {
	return nil, fmt.Errorf("no scheduler client initialized")
}

func (c *schedulerClientNotInitialized) GetJob(ctx context.Context, in *schedulerv1pb.GetJobRequest, opts ...grpc.CallOption) (*schedulerv1pb.GetJobResponse, error) {
	return nil, fmt.Errorf("no scheduler client initialized")
}

func (c *schedulerClientNotInitialized) DeleteJob(ctx context.Context, in *schedulerv1pb.DeleteJobRequest, opts ...grpc.CallOption) (*schedulerv1pb.DeleteJobResponse, error) {
	return nil, fmt.Errorf("no scheduler client initialized")
}

func (c *schedulerClientNotInitialized) WatchJobs(ctx context.Context, opts ...grpc.CallOption) (schedulerv1pb.Scheduler_WatchJobsClient, error) {
	return nil, fmt.Errorf("no scheduler client initialized")
}
