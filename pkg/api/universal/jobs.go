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
	"github.com/dapr/dapr/pkg/messages/errorcodes"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

const (
	rpcTimeout = time.Second * 30
)

func (a *Universal) ScheduleJobAlpha1(ctx context.Context, inReq *runtimev1pb.ScheduleJobRequest) (*runtimev1pb.ScheduleJobResponse, error) {
	return a.scheduleJob(ctx, inReq)
}

func (a *Universal) ScheduleJobAlpha1HTTP(ctx context.Context, job *internalsv1pb.JobHTTPRequest) (*runtimev1pb.ScheduleJobResponse, error) {
	data, err := anypb.New(job.GetData())
	if err != nil {
		return &runtimev1pb.ScheduleJobResponse{}, fmt.Errorf("error creating storable job data from job: %w", err)
	}

	//nolint:protogetter
	return a.scheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:          job.GetName(),
			Schedule:      job.Schedule,
			Repeats:       job.Repeats,
			DueTime:       job.DueTime,
			Ttl:           job.Ttl,
			Data:          data,
			FailurePolicy: job.GetFailurePolicy(),
		},
		Overwrite: job.GetOverwrite(),
	})
}

func (a *Universal) scheduleJob(ctx context.Context, jobRequest *runtimev1pb.ScheduleJobRequest) (*runtimev1pb.ScheduleJobResponse, error) {
	errMetadata := map[string]string{
		"appID":     a.AppID(),
		"namespace": a.Namespace(),
	}
	job := jobRequest.GetJob()
	if job == nil {
		return &runtimev1pb.ScheduleJobResponse{}, apierrors.Empty("Job", errMetadata, errorcodes.SchedulerEmpty)
	}

	if job.GetName() == "" || strings.Contains(job.GetName(), "|") {
		return &runtimev1pb.ScheduleJobResponse{}, apierrors.Empty("Name", errMetadata, errorcodes.SchedulerJobNameEmpty)
	}

	if job.Schedule == nil && job.DueTime == nil {
		return &runtimev1pb.ScheduleJobResponse{}, apierrors.Empty("Schedule", errMetadata, errorcodes.SchedulerScheduleEmpty)
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
		Overwrite: jobRequest.GetOverwrite(),
		//nolint:protogetter
		Job: &schedulerv1pb.Job{
			Schedule:      job.Schedule,
			Data:          job.GetData(),
			Repeats:       job.Repeats,
			DueTime:       job.DueTime,
			Ttl:           job.Ttl,
			FailurePolicy: job.GetFailurePolicy(),
		},
	}

	schedCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	_, err := a.scheduler.ScheduleJob(schedCtx, internalScheduleJobReq, grpc.WaitForReady(true))
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
		return &runtimev1pb.DeleteJobResponse{}, apierrors.Empty("Name", errMetadata, errorcodes.SchedulerJobNameEmpty)
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

	_, err := a.scheduler.DeleteJob(schedCtx, internalDeleteJobReq, grpc.WaitForReady(true))
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
		return new(runtimev1pb.GetJobResponse), apierrors.Empty("Name", errMetadata, errorcodes.SchedulerJobNameEmpty)
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

	resp, err := a.scheduler.GetJob(schedCtx, internalGetJobReq, grpc.WaitForReady(true))
	if err != nil {
		a.logger.Errorf("Error getting job %s due to: %s", inReq.GetName(), err)
		return nil, apierrors.SchedulerGetJob(errMetadata, err)
	}

	return &runtimev1pb.GetJobResponse{
		Job: &runtimev1pb.Job{
			Name:          inReq.GetName(),
			Schedule:      resp.GetJob().Schedule, //nolint:protogetter
			Data:          resp.GetJob().GetData(),
			Repeats:       resp.GetJob().Repeats, //nolint:protogetter
			DueTime:       resp.GetJob().DueTime, //nolint:protogetter
			Ttl:           resp.GetJob().Ttl,     //nolint:protogetter
			FailurePolicy: resp.GetJob().GetFailurePolicy(),
		},
	}, nil
}

func (a *Universal) DeleteJobsByPrefixAlpha1(ctx context.Context, req *runtimev1pb.DeleteJobsByPrefixRequestAlpha1) (*runtimev1pb.DeleteJobsByPrefixResponseAlpha1, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	_, err := a.scheduler.DeleteByNamePrefix(ctx, &schedulerv1pb.DeleteByNamePrefixRequest{
		NamePrefix: req.GetNamePrefix(),
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     a.appID,
			Namespace: a.Namespace(),
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	if err != nil {
		a.logger.Errorf("Error listing jobs due to: %s", err)
		return nil, apierrors.SchedulerDeleteJob(map[string]string{
			"appID":     a.AppID(),
			"namespace": a.Namespace(),
		}, err)
	}

	return nil, nil
}

func (a *Universal) ListJobsAlpha1(ctx context.Context, req *runtimev1pb.ListJobsRequestAlpha1) (*runtimev1pb.ListJobsResponseAlpha1, error) {
	errMetadata := map[string]string{
		"appID":     a.AppID(),
		"namespace": a.Namespace(),
	}

	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	resp, err := a.scheduler.ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     a.appID,
			Namespace: a.Namespace(),
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	if err != nil {
		a.logger.Errorf("Error listing jobs due to: %s", err)
		return nil, apierrors.SchedulerListJobs(errMetadata, err)
	}

	jobs := make([]*runtimev1pb.Job, 0, len(resp.GetJobs()))
	for _, namedJob := range resp.GetJobs() {
		job := namedJob.GetJob()
		//nolint:protogetter
		jobs = append(jobs, &runtimev1pb.Job{
			Name:          namedJob.GetName(),
			Schedule:      job.Schedule,
			Repeats:       job.Repeats,
			DueTime:       job.DueTime,
			Ttl:           job.Ttl,
			Data:          job.Data,
			FailurePolicy: job.FailurePolicy,
		})
	}

	return &runtimev1pb.ListJobsResponseAlpha1{
		Jobs: jobs,
	}, nil
}
