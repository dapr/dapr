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
	errMetadata := map[string]string{
		"appID":     a.AppID(),
		"namespace": a.Namespace(),
	}

	job := inReq.GetJob()

	if job == nil {
		return &emptypb.Empty{}, apierrors.Empty("Job", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.PostFixEmpty))
	}

	if job.GetName() == "" || strings.Contains(job.GetName(), "|") {
		return &emptypb.Empty{}, apierrors.Empty("Name", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	//nolint:protogetter
	if job.Schedule == nil && job.DueTime == nil {
		return &emptypb.Empty{}, apierrors.Empty("Schedule", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixSchedule, apierrors.PostFixEmpty))
	}

	internalScheduleJobReq := &schedulerv1pb.ScheduleJobRequest{
		Name: job.GetName(),
		Metadata: &schedulerv1pb.ScheduleJobMetadata{
			AppId:     a.appID,
			Namespace: a.Namespace(),
			Type: &schedulerv1pb.ScheduleJobMetadataType{
				Type: &schedulerv1pb.ScheduleJobMetadataType_Job{
					Job: new(schedulerv1pb.ScheduleTypeJob),
				},
			},
		},
		Job: &schedulerv1pb.Job{
			Schedule: job.Schedule, //nolint:protogetter
			Data:     job.GetData(),
			Repeats:  job.Repeats, //nolint:protogetter
			DueTime:  job.DueTime, //nolint:protogetter
			Ttl:      job.Ttl,     //nolint:protogetter
		},
	}

	_, err := a.schedulerClients.Next().ScheduleJob(ctx, internalScheduleJobReq)
	if err != nil {
		a.logger.Errorf("Error scheduling job %s", inReq.GetJob().GetName())
		return &emptypb.Empty{}, apierrors.SchedulerScheduleJob(errMetadata, err)
	}

	return &emptypb.Empty{}, nil
}

func (a *Universal) DeleteJob(ctx context.Context, inReq *runtimev1pb.DeleteJobRequest) (*emptypb.Empty, error) {
	errMetadata := map[string]string{
		"appID":     a.AppID(),
		"namespace": a.Namespace(),
	}

	if inReq.GetName() == "" {
		a.logger.Error("Job name is empty.")
		return &emptypb.Empty{}, apierrors.Empty("Name", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	internalDeleteJobReq := &schedulerv1pb.DeleteJobRequest{
		Name: inReq.GetName(),
		Metadata: &schedulerv1pb.ScheduleJobMetadata{
			AppId:     a.appID,
			Namespace: a.Namespace(),
			Type: &schedulerv1pb.ScheduleJobMetadataType{
				Type: &schedulerv1pb.ScheduleJobMetadataType_Job{
					Job: new(schedulerv1pb.ScheduleTypeJob),
				},
			},
		},
	}

	_, err := a.schedulerClients.Next().DeleteJob(ctx, internalDeleteJobReq)
	if err != nil {
		a.logger.Errorf("Error deleting job: %s", inReq.GetName())
		return &emptypb.Empty{}, apierrors.SchedulerDeleteJob(errMetadata, err)
	}

	return &emptypb.Empty{}, nil
}

func (a *Universal) GetJob(ctx context.Context, inReq *runtimev1pb.GetJobRequest) (*runtimev1pb.GetJobResponse, error) {
	errMetadata := map[string]string{
		"appID":     a.AppID(),
		"namespace": a.Namespace(),
	}

	if inReq.GetName() == "" {
		a.logger.Error("Job name is empty.")
		return new(runtimev1pb.GetJobResponse), apierrors.Empty("Name", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	internalGetJobReq := &schedulerv1pb.GetJobRequest{
		Name: inReq.GetName(),
		Metadata: &schedulerv1pb.ScheduleJobMetadata{
			AppId:     a.appID,
			Namespace: a.Namespace(),
			Type: &schedulerv1pb.ScheduleJobMetadataType{
				Type: &schedulerv1pb.ScheduleJobMetadataType_Job{
					Job: new(schedulerv1pb.ScheduleTypeJob),
				},
			},
		},
	}

	resp, err := a.schedulerClients.Next().GetJob(ctx, internalGetJobReq)
	if err != nil {
		a.logger.Errorf("Error getting job %s", inReq.GetName())
		return nil, apierrors.SchedulerGetJob(errMetadata, err)
	}

	return &runtimev1pb.GetJobResponse{
		Job: &runtimev1pb.Job{
			Name:     inReq.GetName(),
			Schedule: resp.GetJob().Schedule, //nolint:protogetter
			Data:     resp.GetJob().GetData(),
			Repeats:  resp.GetJob().Repeats, //nolint:protogetter
			DueTime:  resp.GetJob().DueTime, //nolint:protogetter
			Ttl:      resp.GetJob().Ttl,     //nolint:protogetter
		},
	}, nil
}
