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

package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

// createWorkflowReminder schedules a wake-up reminder on the workflow actor.
// The reminderName is used verbatim: callers that want retries to collapse
// onto a single scheduler entry (overwrite-by-name) must pass a deterministic
// name (e.g. events.EventReminderName(prefix, event)); callers without a
// stable identity must build one with randomReminderName so unrelated
// reminders do not clobber each other.
func (o *orchestrator) createWorkflowReminder(ctx context.Context, reminderName string, data proto.Message, start time.Time, targetAppID string, concurrencyKey *string) error {
	actorType := o.actorTypeBuilder.Workflow(targetAppID)
	return o.createReminderWithType(ctx, reminderName, data, start, actorType, concurrencyKey)
}

// createRetentionReminder creates the retention reminder that triggers
// workflow purge. The name is deterministic so the call is idempotent: the
// scheduler's overwrite-by-name semantics ensure that retrying a Create
// after a transient scheduler failure converges on a single retention
// reminder rather than accumulating duplicates.
func (o *orchestrator) createRetentionReminder(ctx context.Context, name string, start time.Time) (string, error) {
	dueTime := start.UTC().Format(time.RFC3339)

	return name, common.CreateReminderWithRetry(ctx, o.reminders, &actorapi.CreateReminderRequest{
		ActorType: o.retentionActorType,
		ActorID:   o.actorID,
		DueTime:   dueTime,
		Name:      name,
		// One shot, retry forever, every second.
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second),
					MaxRetries: nil,
				},
			},
		},
	})
}

// randomReminderName returns the prefix with a random suffix appended.
// Use for reminders that have no stable identity to deduplicate retries by.
func randomReminderName(prefix string) (string, error) {
	b := make([]byte, 6)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", fmt.Errorf("failed to generate reminder ID: %w", err)
	}
	return prefix + "-" + base64.RawURLEncoding.EncodeToString(b), nil
}

func (o *orchestrator) createReminderWithType(ctx context.Context, reminderName string, data proto.Message, start time.Time, actorType string, concurrencyKey *string) error {
	dueTime := start.UTC().Format(time.RFC3339)

	var adata *anypb.Any
	if data != nil {
		var err error
		adata, err = anypb.New(data)
		if err != nil {
			return err
		}
	}

	log.Debugf("Workflow actor '%s||%s': creating '%s' reminder with DueTime = '%s'", actorType, o.actorID, reminderName, dueTime)

	return common.CreateReminderWithRetry(ctx, o.reminders, &actorapi.CreateReminderRequest{
		ActorType: actorType,
		ActorID:   o.actorID,
		Data:      adata,
		DueTime:   dueTime,
		Name:      reminderName,
		// One shot, retry forever, every second.
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second),
					MaxRetries: nil,
				},
			},
		},
		ConcurrencyKey: concurrencyKey,
	})
}

// deleteAllReminders deletes all reminders for the workflow and its
// activities. This is called when the workflow completes to ensure no orphan
// reminders (e.g. unfired timers) remain in the scheduler.
func (o *orchestrator) deleteAllReminders(ctx context.Context) error {
	actorType := o.actorTypeBuilder.Workflow(o.appID)

	log.Debugf("Workflow actor '%s': deleting all reminders for completed workflow", o.actorID)

	if err := o.reminders.DeleteByActorID(ctx, &actorapi.DeleteRemindersByActorIDRequest{
		ActorType:       actorType,
		ActorID:         o.actorID,
		MatchIDAsPrefix: false,
	}); err != nil {
		return fmt.Errorf("actor '%s' failed to delete reminders on completion: %w", o.actorID, err)
	}

	if err := o.reminders.DeleteByActorID(ctx, &actorapi.DeleteRemindersByActorIDRequest{
		ActorType:       o.activityActorType,
		ActorID:         o.actorID + "::",
		MatchIDAsPrefix: true,
	}); err != nil {
		return fmt.Errorf("actor '%s' failed to delete activity reminders on completion: %w", o.actorID, err)
	}

	return nil
}
