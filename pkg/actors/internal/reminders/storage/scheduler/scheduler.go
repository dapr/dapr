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

package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/migration"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage"
	"github.com/dapr/dapr/pkg/actors/table"
	apierrors "github.com/dapr/dapr/pkg/api/errors"
	"github.com/dapr/dapr/pkg/healthz"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/scheduler/clients"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
	kittime "github.com/dapr/kit/time"
)

var log = logger.NewLogger("dapr.runtime.actor.reminders.scheduler")

type Options struct {
	Namespace     string
	AppID         string
	Clients       *clients.Clients
	StateReminder storage.Interface
	Table         table.Interface
	Healthz       healthz.Healthz
}

// Implements a reminders provider that does nothing when using Scheduler Service.
type scheduler struct {
	namespace     string
	appID         string
	clients       *clients.Clients
	table         table.Interface
	stateReminder storage.Interface
	htarget       healthz.Target
}

func New(opts Options) storage.Interface {
	log.Info("Using Scheduler service for reminders.")
	return &scheduler{
		clients:       opts.Clients,
		namespace:     opts.Namespace,
		appID:         opts.AppID,
		stateReminder: opts.StateReminder,
		table:         opts.Table,
		htarget:       opts.Healthz.AddTarget(),
	}
}

// OnPlacementTablesUpdated is invoked when the actors runtime received an updated placement tables.
func (s *scheduler) OnPlacementTablesUpdated(ctx context.Context, fn func(context.Context, *api.LookupActorRequest) bool) {
	defer s.htarget.Ready()
	err := migration.ToScheduler(ctx, migration.ToSchedulerOptions{
		Table:              s.table,
		StateReminders:     s.stateReminder,
		SchedulerReminders: s,
		LookupFn:           fn,
	})
	if err != nil {
		log.Errorf("Error attempting to migrate reminders to scheduler: %s", err)
	}
}

func (s *scheduler) DrainRebalancedReminders() {}

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

	var dataAny *anypb.Any
	if len(reminder.Data) > 0 {
		buf := &bytes.Buffer{}
		if err = json.Compact(buf, reminder.Data); err != nil {
			return fmt.Errorf("failed to compact reminder %s data: %w", reminder.Name, err)
		}
		dataAny, err = anypb.New(wrapperspb.Bytes(buf.Bytes()))
		if err != nil {
			return err
		}
	}

	var failurePolicy *schedulerv1pb.FailurePolicy
	if reminder.IsOneShot {
		failurePolicy = &schedulerv1pb.FailurePolicy{
			Policy: &schedulerv1pb.FailurePolicy_Constant{
				Constant: &schedulerv1pb.FailurePolicyConstant{
					Interval:   durationpb.New(time.Second),
					MaxRetries: nil,
				},
			},
		}
	}

	internalScheduleJobReq := &schedulerv1pb.ScheduleJobRequest{
		Name: reminder.Name,
		Job: &schedulerv1pb.Job{
			Schedule:      schedule,
			Repeats:       repeats,
			DueTime:       dueTime,
			Ttl:           ttl,
			Data:          dataAny,
			FailurePolicy: failurePolicy,
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

	client, err := s.clients.Next(ctx)
	if err != nil {
		return fmt.Errorf("error getting scheduler client: %w", err)
	}

	_, err = client.ScheduleJob(ctx, internalScheduleJobReq)
	if err != nil {
		log.Errorf("Error scheduling reminder job %s due to: %s", reminder.Name, err)
	}
	return err
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

	client, err := s.clients.Next(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting scheduler client: %w", err)
	}

	job, err := client.GetJob(ctx, internalGetJobReq)
	if err != nil {
		errMetadata := map[string]string{
			"appID":     s.appID,
			"namespace": s.namespace,
			"jobType":   "reminder",
		}
		log.Errorf("Error getting reminder job %s due to: %s", req.Name, err)
		return nil, apierrors.SchedulerGetJob(errMetadata, err)
	}

	var data json.RawMessage
	//nolint:protogetter
	if dd := job.GetJob().Data; dd != nil {
		msg, err := dd.UnmarshalNew()
		if err != nil {
			return nil, err
		}

		switch msg := msg.(type) {
		case *wrapperspb.BytesValue:
			data = msg.Value
		default:
			data, err = protojson.Marshal(msg)
			if err != nil {
				return nil, err
			}
		}
	}

	reminder := &api.Reminder{
		ActorID:   req.ActorID,
		ActorType: req.ActorType,
		Data:      data,
		Period:    api.NewSchedulerReminderPeriod(job.GetJob().GetSchedule(), job.GetJob().GetRepeats()),
		DueTime:   job.GetJob().GetDueTime(),
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

	client, err := s.clients.Next(ctx)
	if err != nil {
		return fmt.Errorf("error getting scheduler client: %w", err)
	}

	_, err = client.DeleteJob(ctx, internalDeleteJobReq)
	if err != nil {
		log.Errorf("Error deleting reminder job %s due to: %s", req.Name, err)
	}

	return err
}

func (s *scheduler) List(ctx context.Context, req *api.ListRemindersRequest) ([]*api.Reminder, error) {
	client, err := s.clients.Next(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := client.ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			AppId:     s.appID,
			Namespace: s.namespace,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: req.ActorType,
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
		jsonBytes, err := protojson.Marshal(job.GetData())
		if err != nil {
			return nil, err
		}

		reminders[i] = &api.Reminder{
			Name:      named.GetName(),
			ActorID:   actor.GetId(),
			ActorType: actor.GetType(),
			Data:      jsonBytes,
			Period:    api.NewSchedulerReminderPeriod(job.GetSchedule(), job.GetRepeats()),
			DueTime:   job.GetDueTime(),
		}
	}
	return reminders, nil
}
