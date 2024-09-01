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
	"google.golang.org/appengine/log"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
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

	apiJob := &api.Job{
		Schedule: job.Schedule, //nolint:protogetter
		DueTime:  job.DueTime,  //nolint:protogetter
		Ttl:      job.Ttl,      //nolint:protogetter
		Repeats:  job.Repeats,  //nolint:protogetter
		Metadata: serialized.Metadata(),
		Payload:  job.GetData(),
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
		return nil, fmt.Errorf("job not found: %s", req.GetName())
	}

	return &schedulerv1pb.GetJobResponse{
		//nolint:protogetter
		Job: &schedulerv1pb.Job{
			Schedule: job.Schedule,
			DueTime:  job.DueTime,
			Ttl:      job.Ttl,
			Repeats:  job.Repeats,
			Data:     job.GetPayload(),
		},
	}, nil
}

func (s *Server) ListJobs(ctx context.Context, req *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	if err := s.authz.Metadata(ctx, req.GetMetadata()); err != nil {
		return nil, err
	}

	prefix, err := buildJobPrefix(req.GetMetadata())
	if err != nil {
		return nil, err
	}

	list, err := s.cron.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to query job list: %w", err)
	}

	jobs := make([]*schedulerv1pb.NamedJob, 0, len(list.GetJobs()))
	for _, job := range list.GetJobs() {
		jobs = append(jobs, &schedulerv1pb.NamedJob{
			Name: job.GetName()[strings.LastIndex(job.GetName(), "||")+2:],
			//nolint:protogetter
			Job: &schedulerv1pb.Job{
				Schedule: job.GetJob().Schedule,
				DueTime:  job.GetJob().DueTime,
				Ttl:      job.GetJob().Ttl,
				Repeats:  job.GetJob().Repeats,
				Data:     job.GetJob().GetPayload(),
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

	s.connectionPool.Add(initial, stream)

	monitoring.RecordSidecarsConnectedCount(1)
	defer monitoring.RecordSidecarsConnectedCount(-1)
	select {
	case <-s.closeCh:
		return errors.New("server is closing")
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
}
