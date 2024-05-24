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

	"google.golang.org/protobuf/types/known/emptypb"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func (a *Universal) ScheduleJob(ctx context.Context, inReq *runtimev1pb.ScheduleJobRequest) (*emptypb.Empty, error) {
	metadata := map[string]string{
		"appId":     a.AppID(),
		"namespace": a.globalConfig.Namespace,
	}

	if inReq.GetJob() == nil {
		return &emptypb.Empty{}, apierrors.Empty("Job", metadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.PostFixEmpty))
	}

	if inReq.GetJob().GetName() == "" || inReq.GetJob().GetName() == " " {
		return &emptypb.Empty{}, apierrors.Empty("Name", metadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	if inReq.GetJob().GetSchedule() == "" {
		return &emptypb.Empty{}, apierrors.Empty("Schedule", metadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixSchedule, apierrors.PostFixEmpty))
	}

	if inReq.GetJob().GetRepeats() < 0 {
		return &emptypb.Empty{}, apierrors.IncorrectNegative("Repeats", metadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixNegative, apierrors.PostFixRepeats))
	}

	jobName := a.globalConfig.Namespace + "||job||" + a.AppID() + "||" + inReq.GetJob().GetName()
	// TODO: add validation on dueTime and ttl

	internalScheduleJobReq := &schedulerv1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     jobName,
			Schedule: inReq.GetJob().GetSchedule(),
			Data:     inReq.GetJob().GetData(),
			Repeats:  inReq.GetJob().GetRepeats(),
			DueTime:  inReq.GetJob().GetDueTime(),
			Ttl:      inReq.GetJob().GetTtl(),
		},
		Metadata:  metadata,                 // TODO: this should generate key if jobStateStore is configured
	}

	_, err := a.schedulerClient.ScheduleJob(ctx, internalScheduleJobReq)
	if err != nil {
		a.logger.Errorf("Error scheduling job %s: %v", inReq.GetJob().GetName(), err)
		return &emptypb.Empty{}, apierrors.SchedulerScheduleJob(metadata, err)
	}

	return &emptypb.Empty{}, nil
}

func (a *Universal) DeleteJob(ctx context.Context, inReq *runtimev1pb.DeleteJobRequest) (*emptypb.Empty, error) {
	metadata := map[string]string{
		"appId":     a.AppID(),
		"namespace": a.globalConfig.Namespace,
	}

	if inReq.GetName() == "" {
		a.logger.Error("Job name is empty.")
		return &emptypb.Empty{}, apierrors.Empty("Name", metadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	jobName := a.globalConfig.Namespace + "||job||" + a.AppID() + "||" + inReq.GetName()
	internalDeleteJobReq := &schedulerv1pb.DeleteJobRequest{
		JobName:  jobName,
		Metadata: metadata,
	}

	_, err := a.schedulerManager.NextClient().DeleteJob(ctx, internalDeleteJobReq)
	if err != nil {
		a.logger.Errorf("Error deleting job: %s", inReq.GetName())
		return &emptypb.Empty{}, apierrors.SchedulerDeleteJob(metadata, err)
	}

	return &emptypb.Empty{}, nil
}

func (a *Universal) GetJob(ctx context.Context, inReq *runtimev1pb.GetJobRequest) (*runtimev1pb.GetJobResponse, error) {
	metadata := map[string]string{
		"appId":     a.AppID(),
		"namespace": a.globalConfig.Namespace,
	}

	response := &runtimev1pb.GetJobResponse{}
	var internalResp *schedulerv1pb.GetJobResponse

	if inReq.GetName() == "" {
		a.logger.Error("Job name is empty.")
		return response, apierrors.Empty("Name", metadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	jobName := a.globalConfig.Namespace + "||job||" + a.AppID() + "||" + inReq.GetName()
	internalGetJobReq := &schedulerv1pb.GetJobRequest{
		JobName:  jobName,
		Metadata: metadata,
	}

	internalResp, err := a.schedulerManager.NextClient().GetJob(ctx, internalGetJobReq)
	if err != nil {
		a.logger.Errorf("Error getting job %s", inReq.GetName())
		return nil, apierrors.SchedulerGetJob(metadata, err)
	}

	response.Job = internalResp.GetJob()

	// Sets the job name back to remove any prefixing done by ourselves in this layer.
	response.Job.Name = inReq.GetName()

	return response, nil
}
