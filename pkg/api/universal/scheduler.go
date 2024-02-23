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
	"strings"

	"google.golang.org/protobuf/types/known/emptypb"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func (a *Universal) ScheduleJob(ctx context.Context, inReq *runtimev1pb.ScheduleJobRequest) (*emptypb.Empty, error) {
	var err error

	metadata := map[string]string{"app_id": a.AppID()}

	if inReq.GetJob() == nil {
		return &emptypb.Empty{}, apierrors.Empty("job", metadata, apierrors.CodePrefixScheduler+apierrors.PostFixEmpty)
	}

	if inReq.GetJob().GetName() == "" {
		return &emptypb.Empty{}, apierrors.Empty("job name", metadata, apierrors.CodePrefixScheduler+apierrors.InFixJob+apierrors.InFixName+apierrors.PostFixEmpty)
	}

	if inReq.GetJob().GetSchedule() == "" {
		return &emptypb.Empty{}, apierrors.Empty("job schedule", metadata, apierrors.CodePrefixScheduler+apierrors.InFixSchedule+apierrors.PostFixEmpty)
	}

	if inReq.GetJob().GetRepeats() < 0 {
		return &emptypb.Empty{}, apierrors.IncorrectNegative("job repeats", metadata, apierrors.CodePrefixScheduler+apierrors.InFixNegative+apierrors.PostFixRepeats)
	}

	// TODO: add validation on dueTime and ttl

	jobName := a.AppID() + "||" + inReq.GetJob().GetName()

	internalScheduleJobReq := &schedulerv1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     jobName,
			Schedule: inReq.GetJob().GetSchedule(),
			Data:     inReq.GetJob().GetData(),
			Repeats:  inReq.GetJob().GetRepeats(),
			DueTime:  inReq.GetJob().GetDueTime(),
			Ttl:      inReq.GetJob().GetTtl(),
		},
		Namespace: "",       // TODO
		Metadata:  metadata, // TODO: this should generate key if jobStateStore is configured
	}

	// TODO: do something with following response?
	_, err = a.schedulerClient.ScheduleJob(ctx, internalScheduleJobReq)
	if err != nil {
		a.logger.Errorf("Error Scheduling job %s. %v", inReq.GetJob().GetName(), err)
		return &emptypb.Empty{}, err
	}

	return &emptypb.Empty{}, err
}

func (a *Universal) DeleteJob(ctx context.Context, inReq *runtimev1pb.DeleteJobRequest) (*emptypb.Empty, error) {
	var err error

	if inReq.GetName() == "" {
		a.logger.Error("Job name empty.")
		return &emptypb.Empty{}, err
	}

	jobName := a.AppID() + "||" + inReq.GetName()
	internalDeleteJobReq := &schedulerv1pb.JobRequest{
		JobName: jobName,
	}

	// TODO: do something with following response?
	_, err = a.schedulerClient.DeleteJob(ctx, internalDeleteJobReq)
	if err != nil {
		a.logger.Errorf("Error Deleting job: %s. %v", inReq.GetName(), err)
		return &emptypb.Empty{}, err
	}

	return &emptypb.Empty{}, err
}

func (a *Universal) GetJob(ctx context.Context, inReq *runtimev1pb.GetJobRequest) (*runtimev1pb.GetJobResponse, error) {
	response := &runtimev1pb.GetJobResponse{}
	var internalResp *schedulerv1pb.GetJobResponse
	var err error

	if inReq.GetName() == "" {
		a.logger.Error("Job name empty.")
		return response, err
	}

	jobName := a.AppID() + "||" + inReq.GetName()
	internalGetJobReq := &schedulerv1pb.JobRequest{
		JobName: jobName,
	}

	internalResp, err = a.schedulerClient.GetJob(ctx, internalGetJobReq)
	if err != nil {
		a.logger.Errorf("Error Getting job %s. %v", inReq.GetName(), err)
		return nil, err
	}

	response.Job = internalResp.GetJob()

	// override job name, so it's the original user's job name and not the app_id prefix
	response.Job.Name = strings.TrimPrefix(jobName, a.AppID()+"||")

	return response, err
}

func (a *Universal) ListJobs(ctx context.Context, inReq *runtimev1pb.ListJobsRequest) (*runtimev1pb.ListJobsResponse, error) {
	response := &runtimev1pb.ListJobsResponse{
		Jobs: []*runtimev1pb.Job{},
	}

	var internalListResp *schedulerv1pb.ListJobsResponse
	var err error

	if inReq.GetAppId() == "" {
		a.logger.Error("Job appID empty.")
		return response, err
	}

	internalListReq := &schedulerv1pb.ListJobsRequest{
		AppId: inReq.GetAppId(),
	}

	internalListResp, err = a.schedulerClient.ListJobs(ctx, internalListReq)
	if err != nil {
		a.logger.Errorf("Error Listing jobs for app %s. %v", inReq.GetAppId(), err)
		return nil, err
	}

	if len(internalListResp.GetJobs()) > 0 {
		response.Jobs = internalListResp.GetJobs()
	}

	for _, job := range response.GetJobs() {
		jobName := job.GetName()
		// override job name, so it's the original user's job name and not the app_id prefix
		job.Name = strings.TrimPrefix(jobName, a.AppID()+"||")
	}

	return response, err
}
