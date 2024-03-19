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
	"strings"

	etcdcron "github.com/Scalingo/go-etcd-cron"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler"
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

	// TODO: figure out if we need/want namespace in job name
	err := s.cron.AddJob(etcdcron.Job{
		Name:     req.GetJob().GetName(),
		Rhythm:   req.GetJob().GetSchedule(),
		Repeats:  req.GetJob().GetRepeats(),
		DueTime:  req.GetJob().GetDueTime(), // TODO: figure out dueTime
		TTL:      req.GetJob().GetTtl(),
		Data:     req.GetJob().GetData(),
		Metadata: req.GetMetadata(), // TODO: do I need this here?
		Func: func(context.Context) error {
			log.Infof("Triggering Job. fixing to send job on s.jobTriggerChan <-")

			s.jobTriggerChan <- req.GetJob() // send job to be consumed and sent to sidecar from WatchJob()

			// TODO: only call below if 'actor' type job
			//innerErr := s.triggerJob(req.GetJob(), req.GetNamespace(), req.GetMetadata())
			//if innerErr != nil {
			//	return innerErr
			//}
			return nil
		},
	})
	if err != nil {
		log.Errorf("error scheduling job %s: %s", req.GetJob().GetName(), err)
		return nil, err
	}

	return &schedulerv1pb.ScheduleJobResponse{}, nil
}

func (s *Server) triggerJob(job *runtimev1pb.Job, namespace string, metadata map[string]string) error {
	_, err := s.TriggerJob(context.Background(), &schedulerv1pb.TriggerJobRequest{
		JobName:   job.GetName(),
		Namespace: namespace,
		Metadata:  metadata,
	})
	if err != nil {
		log.Errorf("error triggering job %s: %s", job.GetName(), err)
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

	entries := s.cron.ListJobsByPrefix(req.GetAppId() + "||")

	jobs := make([]*runtimev1pb.Job, 0, len(entries))
	for _, entry := range entries {
		job := &runtimev1pb.Job{
			Name:     entry.Name,
			Schedule: entry.Rhythm,
			Repeats:  entry.Repeats,
			DueTime:  entry.DueTime,
			Ttl:      entry.TTL,
			Data:     entry.Data,
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

	job := s.cron.GetJob(req.GetJobName())
	if job != nil {
		resp := &schedulerv1pb.GetJobResponse{
			Job: &runtimev1pb.Job{
				Name:     job.Name,
				Schedule: job.Rhythm,
				Repeats:  job.Repeats,
				DueTime:  job.DueTime,
				Ttl:      job.TTL,
				Data:     job.Data,
			},
		}
		return resp, nil
	}

	return nil, fmt.Errorf("job not found")
}

// DeleteJob is a placeholder method that needs to be implemented
func (s *Server) DeleteJob(ctx context.Context, req *schedulerv1pb.JobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.readyCh:
	}

	err := s.cron.DeleteJob(req.GetJobName())
	if err != nil {
		log.Errorf("error deleting job %s: %s", req.GetJobName(), err)
		return nil, err
	}

	return &schedulerv1pb.DeleteJobResponse{}, nil
}

func (s *Server) TriggerJob(ctx context.Context, req *schedulerv1pb.TriggerJobRequest) (*schedulerv1pb.TriggerJobResponse, error) {
	log.Info("Triggering job")
	metadata := req.GetMetadata()
	actorType := metadata["actorType"]
	actorID := metadata["actorId"]
	reminderName := metadata["reminder"]
	if actorType != "" && actorID != "" && reminderName != "" {
		if s.actorRuntime == nil {
			return nil, fmt.Errorf("actor runtime is not configured")
		}

		invokeMethod := "remind/" + reminderName
		contentType := metadata["content-type"]
		invokeReq := internalv1pb.NewInternalInvokeRequest(invokeMethod).
			WithActor(actorType, actorID).
			WithData(req.GetData().GetValue()).
			WithContentType(contentType)

		_, err := s.actorRuntime.Call(ctx, invokeReq)
		return nil, err
	}
	return nil, fmt.Errorf("not implemented")
}

func extractAppID(str string) (string, error) {
	parts := strings.Split(str, "||")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid format: %s", str)
	}
	return parts[1], nil
}

// WatchJob sends jobs to Dapr sidecars upon component changes.
func (s *Server) WatchJob(req *schedulerv1pb.StreamJobRequest, stream schedulerv1pb.Scheduler_WatchJobServer) error {
	errCh := make(chan error)

	// use req details to add sidecar connection details so scheduler knows how many sidecars there are and
	// maintains a conn pool
	s.sidecarConnChan <- &scheduler.SidecarConnDetails{
		Namespace: req.Namespace,
		Host:      req.Hostname,
		Port:      int(req.Port),
		AppID:     req.AppId,
	}
	// TODO: probably add ctx to be passed to the go routine so if that is cancelled the go routine quits
	// Handle job triggers, don't hang scheduler main thread
	go func() {
		defer close(errCh) // Close the error channel when the goroutine exits

		// Listen for job triggers from the channel
		for {
			// Wait for a jobs being triggered
			job := <-s.jobTriggerChan

			jobName, err := extractAppID(job.GetName())
			if err != nil {
				log.Errorf("Error separating job name from appID: %v", err)
				errCh <- err
			}
			// Create a job update
			jobUpdate := &schedulerv1pb.StreamJobResponse{
				Job: &runtimev1pb.Job{
					Name:     jobName,
					Schedule: job.GetSchedule(),
					// TODO: fill rest of fields
				},
			}

			// Send the job update to the sidecar
			if err := stream.Send(jobUpdate); err != nil {
				log.Errorf("Error sending job at trigger time: %v", err)
				errCh <- err
			}
		}
	}()

	// Wait for errors from the goroutine
	select {
	case err := <-errCh:
		return err
	case <-stream.Context().Done(): // sidecar closed stream
		log.Infof("WatchJob stream closed")
		return nil
	}
}
