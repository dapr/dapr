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

package universal

import (
	"context"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Service layer funcs

func (a *Universal) ScheduleJob(ctx context.Context, inReq *runtimev1pb.ScheduleJobRequest) (*emptypb.Empty, error) {
	//validate job details, date, schedule, etc??
	var err error

	internalScheduleJobReq := &schedulerv1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     inReq.GetJob().GetName(),
			Schedule: inReq.GetJob().GetSchedule(),
			Data:     inReq.GetJob().GetData(),
			Repeats:  inReq.GetJob().GetRepeats(),
			DueTime:  inReq.GetJob().GetDueTime(),
			Ttl:      inReq.GetJob().GetTtl(),
		},
		Namespace: "",  //TODO
		Metadata:  nil, //TODO: this should generate key if jobStateStore is configured
	}

	//TODO: do something with following response?
	_, err = a.schedulerClient.ScheduleJob(ctx, internalScheduleJobReq)
	if err != nil {
		a.logger.Errorf("Error Scheduling job %v", err)
		return &emptypb.Empty{}, err
	}

	return &emptypb.Empty{}, err
}

func (a *Universal) DeleteJob(ctx context.Context, inReq *runtimev1pb.DeleteJobRequest) (*emptypb.Empty, error) {
	var err error

	if inReq.GetName() == "" {
		a.logger.Errorf("Job name empty %v", err)
		return &emptypb.Empty{}, err
	}

	internalDeleteJobReq := &schedulerv1pb.JobRequest{
		JobName: inReq.GetName(),
	}

	//TODO: do something with following response?
	_, err = a.schedulerClient.DeleteJob(ctx, internalDeleteJobReq)
	if err != nil {
		a.logger.Errorf("Error Deleting job %v", err)
		return &emptypb.Empty{}, err
	}

	return &emptypb.Empty{}, err
}

func (a *Universal) GetJob(ctx context.Context, inReq *runtimev1pb.GetJobRequest) (*runtimev1pb.GetJobResponse, error) {
	var response *runtimev1pb.GetJobResponse
	var internalResp *schedulerv1pb.GetJobResponse
	var err error

	if inReq.GetName() == "" {
		a.logger.Errorf("Job name empty %v", err)
		return response, err
	}

	internalGetJobReq := &schedulerv1pb.JobRequest{
		JobName: inReq.GetName(),
	}

	internalResp, err = a.schedulerClient.GetJob(ctx, internalGetJobReq)
	if err != nil {
		a.logger.Errorf("Error Getting job %v", err)
		return response, err
	}

	response.Job = internalResp.Job

	return response, err
}

func (a *Universal) ListJobs(ctx context.Context, inReq *runtimev1pb.ListJobsRequest) (*runtimev1pb.ListJobsResponse, error) {
	var response *runtimev1pb.ListJobsResponse
	var internalListResp *schedulerv1pb.ListJobsResponse
	var err error

	if inReq.GetAppId() == "" {
		a.logger.Errorf("Job appID empty %v", err)
		return response, err
	}

	internalListReq := &schedulerv1pb.ListJobsRequest{
		AppId: inReq.GetAppId(),
	}

	internalListResp, err = a.schedulerClient.ListJobs(ctx, internalListReq)
	if err != nil {
		a.logger.Errorf("Error Listing jobs for app %v", err)
		return response, err
	}

	if len(internalListResp.GetJobs()) > 0 {
		response.Jobs = internalListResp.Jobs
	}

	return response, err
}
