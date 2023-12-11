/*
Copyright 2023 The Dapr Authors
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

package universalapi

import (
	"context"
	"fmt"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"google.golang.org/grpc"
	"testing"
)

type mockSchedulerClient struct {
	ConnectHostFunc func(ctx context.Context, req *schedulerv1pb.ConnectHostRequest, opts ...grpc.CallOption) (*schedulerv1pb.ConnectHostResponse, error)
	ScheduleJobFunc func(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest, opts ...grpc.CallOption) (*schedulerv1pb.ScheduleJobResponse, error)
	GetJobFunc      func(ctx context.Context, req *schedulerv1pb.JobRequest, opts ...grpc.CallOption) (*schedulerv1pb.DeleteJobResponse, error)
	DeleteJobFunc   func(ctx context.Context, req *schedulerv1pb.JobRequest, opts ...grpc.CallOption) (*schedulerv1pb.GetJobResponse, error)
	ListJobsFunc    func(ctx context.Context, req *schedulerv1pb.ListJobsRequest, opts ...grpc.CallOption) (*schedulerv1pb.ListJobsResponse, error)
}

func (f *mockSchedulerClient) ScheduleJob(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest, opts ...grpc.CallOption) (*schedulerv1pb.ScheduleJobResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// ListJobs is a placeholder method that needs to be implemented
func (f *mockSchedulerClient) ListJobs(context.Context, *schedulerv1pb.ListJobsRequest, ...grpc.CallOption) (*schedulerv1pb.ListJobsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// GetJob is a placeholder method that needs to be implemented
func (f *mockSchedulerClient) GetJob(context.Context, *schedulerv1pb.JobRequest, ...grpc.CallOption) (*schedulerv1pb.GetJobResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// DeleteJob is a placeholder method that needs to be implemented
func (f *mockSchedulerClient) DeleteJob(context.Context, *schedulerv1pb.JobRequest, ...grpc.CallOption) (*schedulerv1pb.DeleteJobResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *mockSchedulerClient) ConnectHost(context.Context, *schedulerv1pb.ConnectHostRequest, ...grpc.CallOption) (*schedulerv1pb.ConnectHostResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

type mockSchedulerClientWithSchedulerClient struct {
	*mockSchedulerClient
}

func newMockSchedulerClient() *mockSchedulerClientWithSchedulerClient {
	return &mockSchedulerClientWithSchedulerClient{
		mockSchedulerClient: &mockSchedulerClient{},
	}
}

func TestScheduleJob(t *testing.T) {
	//commented below out since still WIP
	/*
		// Create a fake scheduler client for testing
		mockSchedClient := newMockSchedulerClient()

		// Setup Dapr API with the fake scheduler client
		fakeAPI := &UniversalAPI{
			Logger:          testLogger,
			Resiliency:      resiliency.New(nil),
			SchedulerClient: mockSchedClient,
		}

		// Define test cases
		testCases := []struct {
			testName       string
			scheduleJobReq *runtimev1pb.ScheduleJobRequest
			expectedError  codes.Code
		}{
			{
				testName: "Schedule Job Successfully",
				scheduleJobReq: &runtimev1pb.ScheduleJobRequest{
					Job: &runtimev1pb.Job{
						Name:     "test-job",
						Schedule: "@every 1m",
						Data:     nil, //TODO, add test w/ data
						Repeats:  3,
						DueTime:  "10s",
						Ttl:      "11s",
					},
				},
				expectedError: codes.OK,
			},
		}

		for _, tt := range testCases {
			t.Run(tt.testName, func(t *testing.T) {
				ctx := context.Background()

				resp, err := fakeAPI.ScheduleJob(ctx, tt.scheduleJobReq)

				if tt.expectedError != codes.OK {
					require.Error(t, err, "Expected an error")
					assert.Equal(t, tt.expectedError, status.Code(err))
				} else {
					require.NoError(t, err, "Expected no error")
					assert.NotNil(t, resp, "Expected a non-nil response")
				}
			})
		}
	*/
}

func TestDeleteJob(t *testing.T) {

}

func TestGetJob(t *testing.T) {

}

func TestListJobs(t *testing.T) {

}
