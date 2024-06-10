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

package reminders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/internal"
	apierrors "github.com/dapr/dapr/pkg/api/errors"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/runtime/scheduler/clients"
	"github.com/dapr/kit/ptr"
	kittime "github.com/dapr/kit/time"
)

type SchedulerOptions struct {
	Namespace string
	AppID     string
	Clients   *clients.Clients
}

// Implements a reminders provider that does nothing when using Scheduler Service.
type scheduler struct {
	namespace string
	appID     string
	clients   *clients.Clients
}

func NewScheduler(opts SchedulerOptions) internal.RemindersProvider {
	return &scheduler{
		clients:   opts.Clients,
		namespace: opts.Namespace,
		appID:     opts.AppID,
	}
}

func (s *scheduler) SetExecuteReminderFn(fn internal.ExecuteReminderFn) {
}

func (s *scheduler) SetStateStoreProviderFn(fn internal.StateStoreProviderFn) {
}

func (s *scheduler) SetLookupActorFn(fn internal.LookupActorFn) {
}

func (s *scheduler) SetMetricsCollectorFn(fn remindersMetricsCollectorFn) {
}

// OnPlacementTablesUpdated is invoked when the actors runtime received an updated placement tables.
func (s *scheduler) OnPlacementTablesUpdated(ctx context.Context) {
}

func (s *scheduler) DrainRebalancedReminders(actorType string, actorID string) {
}

func (s *scheduler) CreateReminder(ctx context.Context, reminder *internal.CreateReminderRequest) error {
	log.Debug("Using Scheduler service for reminders")
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

	internalScheduleJobReq := &schedulerv1pb.ScheduleJobRequest{
		Name: reminder.Name,
		Job: &schedulerv1pb.Job{
			Schedule: schedule,
			Repeats:  repeats,
			DueTime:  dueTime,
			Ttl:      ttl,
			Data:     dataAny,
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

	_, err = s.clients.Next().ScheduleJob(ctx, internalScheduleJobReq)
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
		repeats = ptr.Of(uint32(repetition))
	}

	return ptr.Of("@every " + duration.String()), repeats, nil
}

func (s *scheduler) Close() error {
	return nil
}

func (s *scheduler) Init(ctx context.Context) error {
	return nil
}

func (s *scheduler) GetReminder(ctx context.Context, req *internal.GetReminderRequest) (*internal.Reminder, error) {
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

	job, err := s.clients.Next().GetJob(ctx, internalGetJobReq)
	if err != nil {
		errMetadata := map[string]string{
			"appID":     s.appID,
			"namespace": s.namespace,
			"jobType":   "reminder",
		}
		log.Errorf("Error getting reminder job %s", req.Name)
		return nil, apierrors.SchedulerGetJob(errMetadata, err)
	}

	jsonBytes, err := protojson.Marshal(job.GetJob().GetData())
	if err != nil {
		return nil, err
	}

	var data json.RawMessage
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return nil, err
	}

	reminder := &internal.Reminder{
		ActorID:   req.ActorID,
		ActorType: req.ActorType,
		Data:      data,
		Period:    internal.NewSchedulerReminderPeriod(job.GetJob().GetSchedule(), job.GetJob().GetRepeats()),
		DueTime:   job.GetJob().GetDueTime(),
	}

	return reminder, nil
}

func (s *scheduler) DeleteReminder(ctx context.Context, req internal.DeleteReminderRequest) error {
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

	_, err := s.clients.Next().DeleteJob(ctx, internalDeleteJobReq)
	return err
}
