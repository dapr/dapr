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
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

const (
	rpcTimeout = time.Second * 30
)

func (a *Universal) ScheduleJobAlpha1(ctx context.Context, inReq *runtimev1pb.ScheduleJobRequest) (*runtimev1pb.ScheduleJobResponse, error) {
	return a.scheduleJob(ctx, inReq.GetJob())
}

func (a *Universal) ScheduleJobAlpha1HTTP(ctx context.Context, job *internalsv1pb.JobHTTPRequest) (*runtimev1pb.ScheduleJobResponse, error) {
	data, err := anypb.New(job.GetData())
	if err != nil {
		return &runtimev1pb.ScheduleJobResponse{}, fmt.Errorf("error creating storable job data from job: %w", err)
	}

	//nolint:protogetter
	return a.scheduleJob(ctx, &runtimev1pb.Job{
		Name:     job.GetName(),
		Schedule: job.Schedule,
		Repeats:  job.Repeats,
		DueTime:  job.DueTime,
		Ttl:      job.Ttl,
		Data:     data,
	})
}

func (a *Universal) scheduleJob(ctx context.Context, job *runtimev1pb.Job) (*runtimev1pb.ScheduleJobResponse, error) {
	errMetadata := map[string]string{
		"appID":     a.AppID(),
		"namespace": a.Namespace(),
	}

	if job == nil {
		return &runtimev1pb.ScheduleJobResponse{}, apierrors.Empty("Job", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.PostFixEmpty))
	}

	if job.GetName() == "" || strings.Contains(job.GetName(), "|") {
		return &runtimev1pb.ScheduleJobResponse{}, apierrors.Empty("Name", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	if job.Schedule == nil && job.DueTime == nil {
		return &runtimev1pb.ScheduleJobResponse{}, apierrors.Empty("Schedule", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixSchedule, apierrors.PostFixEmpty))
	}

	internalScheduleJobReq := &schedulerv1pb.ScheduleJobRequest{
		Name: job.GetName(),
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     a.appID,
			Namespace: a.Namespace(),
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
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

	schedCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := a.schedulerClients.Next(ctx)
	if err != nil {
		a.logger.Errorf("Error getting scheduler client: %s", err)
		return &runtimev1pb.ScheduleJobResponse{}, apierrors.SchedulerScheduleJob(errMetadata, err)
	}

	_, err = client.ScheduleJob(schedCtx, internalScheduleJobReq, grpc.WaitForReady(true))
	if err != nil {
		a.logger.Errorf("Error scheduling job %s due to: %s", job.GetName(), err)
		return &runtimev1pb.ScheduleJobResponse{}, apierrors.SchedulerScheduleJob(errMetadata, err)
	}

	return &runtimev1pb.ScheduleJobResponse{}, nil
}

func (a *Universal) DeleteJobAlpha1(ctx context.Context, inReq *runtimev1pb.DeleteJobRequest) (*runtimev1pb.DeleteJobResponse, error) {
	errMetadata := map[string]string{
		"appID":     a.AppID(),
		"namespace": a.Namespace(),
	}

	if inReq.GetName() == "" {
		a.logger.Error("Job name is empty.")
		return &runtimev1pb.DeleteJobResponse{}, apierrors.Empty("Name", errMetadata, apierrors.ConstructReason(apierrors.CodePrefixScheduler, apierrors.InFixJob, apierrors.InFixName, apierrors.PostFixEmpty))
	}

	internalDeleteJobReq := &schedulerv1pb.DeleteJobRequest{
		Name: inReq.GetName(),
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     a.appID,
			Namespace: a.Namespace(),
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	}

	schedCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := a.schedulerClients.Next(ctx)
	if err != nil {
		a.logger.Errorf("Error getting scheduler client: %s", err)
		return &runtimev1pb.DeleteJobResponse{}, apierrors.SchedulerDeleteJob(errMetadata, err)
	}

	_, err = client.DeleteJob(schedCtx, internalDeleteJobReq, grpc.WaitForReady(true))
	if err != nil {
		a.logger.Errorf("Error deleting job: %s due to: %s", inReq.GetName(), err)
		return &runtimev1pb.DeleteJobResponse{}, apierrors.SchedulerDeleteJob(errMetadata, err)
	}

	return &runtimev1pb.DeleteJobResponse{}, nil
}

func (a *Universal) GetJobAlpha1(ctx context.Context, inReq *runtimev1pb.GetJobRequest) (*runtimev1pb.GetJobResponse, error) {
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
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     a.appID,
			Namespace: a.Namespace(),
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	}

	schedCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := a.schedulerClients.Next(ctx)
	if err != nil {
		a.logger.Errorf("Error getting scheduler client: %s", err)
		return nil, apierrors.SchedulerGetJob(errMetadata, err)
	}

	resp, err := client.GetJob(schedCtx, internalGetJobReq, grpc.WaitForReady(true))
	if err != nil {
		a.logger.Errorf("Error getting job %s due to: %s", inReq.GetName(), err)
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
