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

package server

import (
	"context"
	"fmt"

	etcdcron "github.com/Scalingo/go-etcd-cron"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func (s *Server) ConnectHost(context.Context, *schedulerv1pb.ConnectHostRequest) (*schedulerv1pb.ConnectHostResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// ScheduleJob is a placeholder method that needs to be implemented
func (s *Server) ScheduleJob(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	err := s.cron.AddJob(etcdcron.Job{
		Name:   req.Job.Name,
		Rhythm: req.Job.Schedule,
		Func: func(context.Context) error {
			innerErr := s.triggerJob(req.Job, req.Namespace, req.Metadata)
			if innerErr != nil {
				return innerErr
			}
			return nil
		},
	})
	if err != nil {
		log.Errorf("error scheduling job %s: %s", req.Job.Name, err)
		return nil, err
	}

	return &schedulerv1pb.ScheduleJobResponse{}, nil
}

func (s *Server) triggerJob(job *runtimev1pb.Job, namespace string, metadata map[string]string) error {
	_, err := s.TriggerJob(context.Background(), &schedulerv1pb.TriggerJobRequest{
		JobName:   job.Name,
		Namespace: namespace,
		Metadata:  metadata,
	})
	if err != nil {
		log.Errorf("error triggering job %s: %s", job.Name, err)
		return err
	}
	return nil
}

// ListJobs is a placeholder method that needs to be implemented
func (s *Server) ListJobs(ctx context.Context, req *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	// TODO: need to do some tweaks here to get entries by appID from req.AppId

	entries := s.cron.Entries()

	var jobs []*runtimev1pb.Job
	for _, entry := range entries {
		job := &runtimev1pb.Job{
			Name:     entry.Job.Name,
			Schedule: entry.Job.Rhythm,
			// TODO: rest
			//Data:     nil,
			//Repeats:  0,
			//DueTime:  "",
			//Ttl:      "",
		}
		jobs = append(jobs, job)
	}

	resp := &schedulerv1pb.ListJobsResponse{Jobs: jobs}

	return resp, nil
}

// GetJob is a placeholder method that needs to be implemented
func (s *Server) GetJob(ctx context.Context, req *schedulerv1pb.JobRequest) (*schedulerv1pb.GetJobResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	return nil, fmt.Errorf("not implemented")
}

// DeleteJob is a placeholder method that needs to be implemented
func (s *Server) DeleteJob(ctx context.Context, req *schedulerv1pb.JobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	return nil, fmt.Errorf("not implemented")
}

func (s *Server) TriggerJob(context.Context, *schedulerv1pb.TriggerJobRequest) (*schedulerv1pb.TriggerJobResponse, error) {
	log.Info("Triggering job")
	return nil, fmt.Errorf("not implemented")
}
