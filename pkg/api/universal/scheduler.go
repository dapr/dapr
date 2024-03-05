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
	errMetadata := map[string]string{"app_id": a.AppID()}

	if inReq.GetJob() == nil {
		return &emptypb.Empty{}, apierrors.Empty("Job", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.PostFixEmpty))
	}

	if inReq.GetJob().GetName() == "" || inReq.GetJob().GetName() == " " {
		return &emptypb.Empty{}, apierrors.Empty("Name", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	if inReq.GetJob().GetSchedule() == "" {
		return &emptypb.Empty{}, apierrors.Empty("Schedule", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixSchedule, apierrors.PostFixEmpty))
	}

	if inReq.GetJob().GetRepeats() < 0 {
		return &emptypb.Empty{}, apierrors.IncorrectNegative("Repeats", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixNegative, apierrors.PostFixRepeats))
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
		Namespace: "",  // TODO
		Metadata:  nil, // TODO: this should generate key if jobStateStore is configured
	}

	// TODO: do something with following response?
	_, err := a.schedulerClient.ScheduleJob(ctx, internalScheduleJobReq)
	if err != nil {
		a.logger.Errorf("Error scheduling job %s", inReq.GetJob().GetName())
		return &emptypb.Empty{}, apierrors.SchedulerScheduleJob(errMetadata, err)
	}

	return &emptypb.Empty{}, nil
}

func (a *Universal) DeleteJob(ctx context.Context, inReq *runtimev1pb.DeleteJobRequest) (*emptypb.Empty, error) {
	errMetadata := map[string]string{"app_id": a.AppID()}

	if inReq.GetName() == "" {
		a.logger.Error("Job name is empty.")
		return &emptypb.Empty{}, apierrors.Empty("Name", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	jobName := a.AppID() + "||" + inReq.GetName()
	internalDeleteJobReq := &schedulerv1pb.JobRequest{
		JobName: jobName,
	}

	_, err := a.schedulerClient.DeleteJob(ctx, internalDeleteJobReq)
	if err != nil {
		a.logger.Errorf("Error deleting job: %s", inReq.GetName())
		return &emptypb.Empty{}, apierrors.SchedulerDeleteJob(errMetadata, err)
	}

	return &emptypb.Empty{}, nil
}

func (a *Universal) GetJob(ctx context.Context, inReq *runtimev1pb.GetJobRequest) (*runtimev1pb.GetJobResponse, error) {
	errMetadata := map[string]string{"app_id": a.AppID()}

	response := &runtimev1pb.GetJobResponse{}
	var internalResp *schedulerv1pb.GetJobResponse

	if inReq.GetName() == "" {
		a.logger.Error("Job name is empty.")
		return response, apierrors.Empty("Name", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	jobName := a.AppID() + "||" + inReq.GetName()
	internalGetJobReq := &schedulerv1pb.JobRequest{
		JobName: jobName,
	}

	internalResp, err := a.schedulerClient.GetJob(ctx, internalGetJobReq)
	if err != nil {
		a.logger.Errorf("Error getting job %s", inReq.GetName())
		return nil, apierrors.SchedulerGetJob(errMetadata, err)
	}

	response.Job = internalResp.GetJob()

	// override job name, so it's the original user's job name and not the app_id prefix
	response.Job.Name = strings.TrimPrefix(jobName, a.AppID()+"||")

	return response, nil
}

func (a *Universal) ListJobs(ctx context.Context, inReq *runtimev1pb.ListJobsRequest) (*runtimev1pb.ListJobsResponse, error) {
	response := &runtimev1pb.ListJobsResponse{
		Jobs: []*runtimev1pb.Job{},
	}

	if inReq.GetAppId() == "" {
		a.logger.Error("Job appID empty.")
		return response, apierrors.Empty("AppID", nil, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixAppID, apierrors.PostFixEmpty))
	}

	internalListReq := &schedulerv1pb.ListJobsRequest{
		AppId: inReq.GetAppId(),
	}

	internalListResp, err := a.schedulerClient.ListJobs(ctx, internalListReq)
	if err != nil {
		a.logger.Errorf("Error listing jobs for app %s", inReq.GetAppId())
		return nil, apierrors.SchedulerListJobs(map[string]string{"app_id": a.AppID()}, err)
	}

	if len(internalListResp.GetJobs()) > 0 {
		response.Jobs = internalListResp.GetJobs()
	}

	for _, job := range response.GetJobs() {
		jobName := job.GetName()
		// override job name, so it's the original user's job name and not the app_id prefix
		job.Name = strings.TrimPrefix(jobName, a.AppID()+"||")
	}

	return response, nil
}
