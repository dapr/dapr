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

package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/diagridio/go-etcd-cron/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/serialize"
)

func (s *Server) ScheduleJob(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	cron, err := s.cron.Client(ctx)
	if err != nil {
		return nil, err
	}

	serialized, err := s.serializer.FromRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	job := req.GetJob()

	//nolint:protogetter
	apiJob := &api.Job{
		Schedule:      job.Schedule,
		DueTime:       job.DueTime,
		Ttl:           job.Ttl,
		Repeats:       job.Repeats,
		Metadata:      serialized.Metadata(),
		Payload:       job.GetData(),
		FailurePolicy: schedFPToCron(job.FailurePolicy),
	}

	err = cron.Add(ctx, serialized.Name(), apiJob)
	if err != nil {
		log.Errorf("error scheduling job %s: %s", req.GetName(), err)
		return nil, err
	}
	monitoring.RecordJobsScheduledCount(req.GetMetadata())
	return &schedulerv1pb.ScheduleJobResponse{}, nil
}

func (s *Server) DeleteJob(ctx context.Context, req *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	cron, err := s.cron.Client(ctx)
	if err != nil {
		return nil, err
	}

	job, err := s.serializer.FromRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	err = cron.Delete(ctx, job.Name())
	if err != nil {
		log.Errorf("error deleting job %s: %s", job.Name(), err)
		return nil, err
	}

	return &schedulerv1pb.DeleteJobResponse{}, nil
}

func (s *Server) GetJob(ctx context.Context, req *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error) {
	cron, err := s.cron.Client(ctx)
	if err != nil {
		return nil, err
	}

	serialized, err := s.serializer.FromRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	job, err := cron.Get(ctx, serialized.Name())
	if err != nil {
		log.Errorf("error getting job %s: %s", serialized.Name(), err)
		return nil, err
	}

	if job == nil {
		return nil, status.Error(codes.NotFound, "job not found: "+req.GetName())
	}

	return &schedulerv1pb.GetJobResponse{
		//nolint:protogetter
		Job: &schedulerv1pb.Job{
			Schedule:      job.Schedule,
			DueTime:       job.DueTime,
			Ttl:           job.Ttl,
			Repeats:       job.Repeats,
			Data:          job.GetPayload(),
			FailurePolicy: cronFPToSched(job.FailurePolicy),
		},
	}, nil
}

func (s *Server) ListJobs(ctx context.Context, req *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
	cron, err := s.cron.Client(ctx)
	if err != nil {
		return nil, err
	}

	prefix, err := s.serializer.PrefixFromList(ctx, req.GetMetadata())
	if err != nil {
		return nil, err
	}

	list, err := cron.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to query job list: %w", err)
	}

	jobs := make([]*schedulerv1pb.NamedJob, 0, len(list.GetJobs()))
	for _, job := range list.GetJobs() {
		meta, err := serialize.MetadataFromKey(job.GetName())
		if err != nil {
			return nil, fmt.Errorf("failed to parse job metadata: %w", err)
		}

		j := job.GetJob()
		jobs = append(jobs, &schedulerv1pb.NamedJob{
			Name:     job.GetName()[strings.LastIndex(job.GetName(), "||")+2:],
			Metadata: meta,
			//nolint:protogetter
			Job: &schedulerv1pb.Job{
				Schedule:      j.Schedule,
				DueTime:       j.DueTime,
				Ttl:           j.Ttl,
				Repeats:       j.Repeats,
				Data:          j.GetPayload(),
				FailurePolicy: cronFPToSched(j.FailurePolicy),
			},
		})
	}

	return &schedulerv1pb.ListJobsResponse{
		Jobs: jobs,
	}, nil
}

// WatchJobs sends jobs to Dapr sidecars upon component changes.
func (s *Server) WatchJobs(stream schedulerv1pb.Scheduler_WatchJobsServer) error {
	initial, err := s.serializer.FromWatch(stream)
	if err != nil {
		return err
	}

	ctx, err := s.cron.JobsWatch(initial, stream)
	if err != nil {
		return err
	}

	monitoring.RecordSidecarsConnectedCount(1)
	defer monitoring.RecordSidecarsConnectedCount(-1)
	select {
	case <-s.closeCh:
		return errors.New("server is closing")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WatchHosts sends the current set of hosts in the scheduler cluster, and
// updates the sidecars upon changes.
func (s *Server) WatchHosts(_ *schedulerv1pb.WatchHostsRequest, stream schedulerv1pb.Scheduler_WatchHostsServer) error {
	return s.cron.HostsWatch(stream)
}

//nolint:protogetter
func schedFPToCron(fp *schedulerv1pb.FailurePolicy) *api.FailurePolicy {
	if fp == nil {
		return nil
	}

	switch fp.GetPolicy().(type) {
	case *schedulerv1pb.FailurePolicy_Constant:
		return &api.FailurePolicy{
			Policy: &api.FailurePolicy_Constant{
				Constant: &api.FailurePolicyConstant{
					Interval:   fp.GetConstant().Interval,
					MaxRetries: fp.GetConstant().MaxRetries,
				},
			},
		}
	case *schedulerv1pb.FailurePolicy_Drop:
		return &api.FailurePolicy{
			Policy: &api.FailurePolicy_Drop{
				Drop: new(api.FailurePolicyDrop),
			},
		}

	default:
		return nil
	}
}

//nolint:protogetter
func cronFPToSched(fp *api.FailurePolicy) *schedulerv1pb.FailurePolicy {
	if fp == nil {
		return nil
	}

	switch fp.GetPolicy().(type) {
	case *api.FailurePolicy_Constant:
		return &schedulerv1pb.FailurePolicy{
			Policy: &schedulerv1pb.FailurePolicy_Constant{
				Constant: &schedulerv1pb.FailurePolicyConstant{
					Interval:   fp.GetConstant().Interval,
					MaxRetries: fp.GetConstant().MaxRetries,
				},
			},
		}
	case *api.FailurePolicy_Drop:
		return &schedulerv1pb.FailurePolicy{
			Policy: &schedulerv1pb.FailurePolicy_Drop{
				Drop: new(schedulerv1pb.FailurePolicyDrop),
			},
		}

	default:
		return nil
	}
}
