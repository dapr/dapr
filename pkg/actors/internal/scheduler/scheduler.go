/*
Copyright 2025 The Dapr Authors
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

package scheduler

import (
	"context"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/table"
	apierrors "github.com/dapr/dapr/pkg/api/errors"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
	kittime "github.com/dapr/kit/time"
)

var log = logger.NewLogger("dapr.runtime.actor.reminders.scheduler")

// Interface is the interface for the object that provides reminders backend
// storage.
type Interface interface {
	io.Closer

	Get(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error)
	Create(ctx context.Context, req *api.CreateReminderRequest) error
	Delete(ctx context.Context, req *api.DeleteReminderRequest) error
	DeleteByActorID(ctx context.Context, req *api.DeleteRemindersByActorIDRequest) error
	List(ctx context.Context, req *api.ListRemindersRequest) ([]*api.Reminder, error)
}

type Options struct {
	Namespace string
	AppID     string
	Client    schedulerv1pb.SchedulerClient
	Table     table.Interface
}

type scheduler struct {
	namespace string
	appID     string
	client    schedulerv1pb.SchedulerClient
	table     table.Interface
}

func New(opts Options) Interface {
	log.Info("Using Scheduler service for reminders.")
	return &scheduler{
		client:    opts.Client,
		namespace: opts.Namespace,
		appID:     opts.AppID,
		table:     opts.Table,
	}
}

func (s *scheduler) Create(ctx context.Context, reminder *api.CreateReminderRequest) error {
	var dueTime *string
	if len(reminder.DueTime) > 0 {
		dueTime = ptr.Of(reminder.DueTime)
	}
	var ttl *string
	if len(reminder.TTL) > 0 {
		ttl = ptr.Of(reminder.TTL)
	}

	schedule, repeats, err := scheduleFromPeriod(reminder.Period)
	if err != nil {
		return err
	}

	overwrite := true
	if reminder.Overwrite != nil {
		overwrite = *reminder.Overwrite
	}

	internalScheduleJobReq := &schedulerv1pb.ScheduleJobRequest{
		Name:      reminder.Name,
		Overwrite: overwrite,
		Job: &schedulerv1pb.Job{
			Schedule:      schedule,
			Repeats:       repeats,
			DueTime:       dueTime,
			Ttl:           ttl,
			Data:          reminder.Data,
			FailurePolicy: reminder.FailurePolicy,
		},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     s.appID,
			Namespace: s.namespace,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Id:   reminder.ActorID,
						Type: reminder.ActorType,
					},
				},
			},
		},
	}

	_, err = s.client.ScheduleJob(ctx, internalScheduleJobReq)
	if err != nil {
		log.Errorf("Error scheduling reminder job %s due to: %s", reminder.Name, err)
		return err
	}

	return nil
}

func scheduleFromPeriod(period string) (*string, *uint32, error) {
	if len(period) == 0 {
		return nil, nil, nil
	}

	years, months, days, duration, repetition, err := kittime.ParseDuration(period)
	if err != nil {
		return nil, nil, fmt.Errorf("unsupported period format: %s", period)
	}

	if years > 0 || months > 0 || days > 0 {
		return nil, nil, fmt.Errorf("unsupported period format: %s", period)
	}

	var repeats *uint32
	if repetition > 0 {
		//TODO: fix types
		//nolint:gosec
		repeats = ptr.Of(uint32(repetition))
	}

	return ptr.Of("@every " + duration.String()), repeats, nil
}

func (s *scheduler) Close() error {
	return nil
}

func (s *scheduler) Get(ctx context.Context, req *api.GetReminderRequest) (*api.Reminder, error) {
	internalGetJobReq := &schedulerv1pb.GetJobRequest{
		Name: req.Name,
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     s.appID,
			Namespace: s.namespace,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Id:   req.ActorID,
						Type: req.ActorType,
					},
				},
			},
		},
	}

	job, err := s.client.GetJob(ctx, internalGetJobReq)
	if err != nil {
		errMetadata := map[string]string{
			"appID":     s.appID,
			"namespace": s.namespace,
			"jobType":   "reminder",
		}
		log.Debugf("Error getting reminder job %s due to: %s", req.Name, err)

		if status, ok := status.FromError(err); ok && status.Code() == codes.NotFound {
			return nil, nil
		}

		return nil, apierrors.SchedulerGetJob(errMetadata, err)
	}

	var expirationTime time.Time
	if job.Job.Ttl != nil {
		expirationTime, err = time.Parse(time.RFC3339, job.GetJob().GetTtl())
		if err != nil {
			log.Errorf("Error parsing expiration time for reminder job %s due to: %s", req.Name, err)
		}
	}

	reminder := &api.Reminder{
		ActorID:        req.ActorID,
		ActorType:      req.ActorType,
		Data:           job.GetJob().GetData(),
		Period:         api.NewSchedulerReminderPeriod(job.GetJob().GetSchedule(), job.GetJob().GetRepeats()),
		DueTime:        job.GetJob().GetDueTime(),
		ExpirationTime: expirationTime,
	}

	return reminder, nil
}

func (s *scheduler) Delete(ctx context.Context, req *api.DeleteReminderRequest) error {
	internalDeleteJobReq := &schedulerv1pb.DeleteJobRequest{
		Name: req.Name,
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     s.appID,
			Namespace: s.namespace,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Id:   req.ActorID,
						Type: req.ActorType,
					},
				},
			},
		},
	}

	_, err := s.client.DeleteJob(ctx, internalDeleteJobReq)
	if err != nil {
		log.Errorf("Error deleting reminder job %s due to: %s", req.Name, err)
		return err
	}

	return nil
}

func (s *scheduler) DeleteByActorID(ctx context.Context, req *api.DeleteRemindersByActorIDRequest) error {
	_, err := s.client.DeleteByMetadata(ctx, &schedulerv1pb.DeleteByMetadataRequest{
		IdPrefixMatch: ptr.Of(req.MatchIDAsPrefix),
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     s.appID,
			Namespace: s.namespace,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Id:   req.ActorID,
						Type: req.ActorType,
					},
				},
			},
		},
	})
	if err != nil {
		log.Errorf("Error deleting reminders for actor %s of type %s due to: %s", req.ActorID, req.ActorType, err)
		return err
	}

	return nil
}

func (s *scheduler) List(ctx context.Context, req *api.ListRemindersRequest) ([]*api.Reminder, error) {
	var id string
	if req.ActorID != nil {
		id = *req.ActorID
	}

	resp, err := s.client.ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     s.appID,
			Namespace: s.namespace,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: req.ActorType,
						Id:   id,
					},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	reminders := make([]*api.Reminder, len(resp.GetJobs()))
	for i, named := range resp.GetJobs() {
		actor := named.GetMetadata().GetTarget().GetActor()
		if actor == nil {
			log.Warnf("Skipping reminder job %s with unsupported target type %s", named.GetName(), named.GetMetadata().GetTarget().String())
			continue
		}

		job := named.GetJob()

		reminders[i] = &api.Reminder{
			Name:      named.GetName(),
			ActorID:   actor.GetId(),
			ActorType: actor.GetType(),
			Data:      job.GetData(),
			Period:    api.NewSchedulerReminderPeriod(job.GetSchedule(), job.GetRepeats()),
			DueTime:   job.GetDueTime(),
		}
	}
	return reminders, nil
}
