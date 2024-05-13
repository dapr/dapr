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
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal"
)

func (s *Server) ScheduleJob(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	if err := s.authz.Metadata(ctx, req.GetMetadata()); err != nil {
		return nil, err
	}

	jobName, err := buildJobName(req.GetName(), req.GetMetadata())
	if err != nil {
		return nil, err
	}

	meta, err := anypb.New(req.GetMetadata())
	if err != nil {
		return nil, err
	}

	job := &api.Job{
		Schedule: req.GetJob().Schedule, //nolint:protogetter
		DueTime:  req.GetJob().DueTime,  //nolint:protogetter
		Ttl:      req.GetJob().Ttl,      //nolint:protogetter
		Repeats:  req.GetJob().Repeats,  //nolint:protogetter
		Metadata: meta,
		Payload:  req.GetJob().GetData(),
	}

	err = s.cron.Add(ctx, jobName, job)
	if err != nil {
		log.Errorf("error scheduling job %s: %s", req.GetName(), err)
		return nil, err
	}

	return &schedulerv1pb.ScheduleJobResponse{}, nil
}

func (s *Server) DeleteJob(ctx context.Context, req *schedulerv1pb.DeleteJobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	if err := s.authz.Metadata(ctx, req.GetMetadata()); err != nil {
		return nil, err
	}

	jobName, err := buildJobName(req.GetName(), req.GetMetadata())
	if err != nil {
		return nil, err
	}

	err = s.cron.Delete(ctx, jobName)
	if err != nil {
		log.Errorf("error deleting job %s: %s", jobName, err)
		return nil, err
	}

	return &schedulerv1pb.DeleteJobResponse{}, nil
}

func (s *Server) GetJob(ctx context.Context, req *schedulerv1pb.GetJobRequest) (*schedulerv1pb.GetJobResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	if err := s.authz.Metadata(ctx, req.GetMetadata()); err != nil {
		return nil, err
	}

	jobName, err := buildJobName(req.GetName(), req.GetMetadata())
	if err != nil {
		return nil, err
	}

	job, err := s.cron.Get(ctx, jobName)
	if err != nil {
		log.Errorf("error getting job %s: %s", jobName, err)
		return nil, err
	}

	if job == nil {
		return nil, fmt.Errorf("job not found: %s", jobName)
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

// WatchJobs sends jobs to Dapr sidecars upon component changes.
func (s *Server) WatchJobs(stream schedulerv1pb.Scheduler_WatchJobsServer) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}

	if req.GetInitial() == nil {
		return errors.New("initial request is required on stream connection")
	}

	if err := s.authz.Initial(stream.Context(), req.GetInitial()); err != nil {
		return err
	}

	s.connectionPool.Add(req.GetInitial(), stream)

	select {
	case <-s.closeCh:
		return errors.New("server is closing")
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
}

func (s *Server) triggerJob(ctx context.Context, req *api.TriggerRequest) bool {
	log.Debugf("Triggering job: %s", req.GetName())

	ctx, cancel := context.WithTimeout(ctx, time.Second*45)
	defer cancel()

	var meta schedulerv1pb.ScheduleJobMetadata
	if err := req.GetMetadata().UnmarshalTo(&meta); err != nil {
		log.Errorf("Error unmarshalling metadata: %s", err)
		return true
	}

	idx := strings.LastIndex(req.GetName(), "||")
	if idx == -1 || len(req.GetName()) <= idx+2 {
		log.Errorf("Job name is malformed: %s", req.GetName())
		return true
	}

	if err := s.connectionPool.Send(ctx, &internal.JobEvent{
		Name:     req.GetName()[idx+2:],
		Data:     req.GetPayload(),
		Metadata: &meta,
	}); err != nil {
		// TODO: add job to a queue or something to try later this should be
		// another long running go routine that accepts this job on a channel
		log.Errorf("Error sending job to connection stream: %s", err)
	}

	return true
}

func buildJobName(name string, meta *schedulerv1pb.ScheduleJobMetadata) (string, error) {
	joinStrings := func(ss ...string) string {
		return strings.Join(ss, "||")
	}

	switch t := meta.GetType(); t.GetType().(type) {
	case *schedulerv1pb.ScheduleJobMetadataType_Actor:
		actor := t.GetActor()
		return joinStrings("actorreminder", meta.GetNamespace(), actor.GetType(), actor.GetId(), name), nil
	case *schedulerv1pb.ScheduleJobMetadataType_Job:
		return joinStrings("app", meta.GetNamespace(), meta.GetAppId(), name), nil
	default:
		return "", fmt.Errorf("unknown job type: %v", t)
	}
}
