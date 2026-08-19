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

package activity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// activityReminderName is the constant name of the per-activity-actor
// execution reminder. One reminder per actor: retries and the drive-failure
// escalation collapse onto a single scheduler entry (overwrite-by-name).
const activityReminderName = "run-activity"

func (a *activity) createReminder(ctx context.Context, invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string) error {
	return a.createActivityReminder(ctx, a.actorID, invocation, dueTime, activityName)
}

// createActivityReminder lives on the factory (with an explicit actorID)
// rather than the *activity because the drive-failure escalation path may
// outlive the actor object (HaltAll recycles it).
func (f *factory) createActivityReminder(ctx context.Context, actorID string, invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string) error {
	log.Debugf("Activity actor '%s||%s': creating reminder '%s' with dueTime=%s", f.actorType, actorID, activityReminderName, dueTime)

	anydata, err := anypb.New(invocation)
	if err != nil {
		return err
	}

	// The activity actor should always create reminders for its own actor type
	// and ID
	return common.CreateReminderWithRetry(ctx, f.reminders, &actorapi.CreateReminderRequest{
		ActorType: f.actorType,
		ActorID:   actorID,
		DueTime:   dueTime.Format(time.RFC3339Nano),
		Name:      activityReminderName,
		// One shot, retry forever, jittered interval.
		FailurePolicy:  common.RetryForeverPolicy(),
		Data:           anydata,
		ConcurrencyKey: activityName,
	})
}

func (f *factory) createWorkflowResultReminder(ctx context.Context, wfActorType, wfActorID string, result *backend.HistoryEvent) error {
	b := make([]byte, 6)
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		return fmt.Errorf("failed to generate reminder ID: %w", err)
	}

	reminderName := common.ReminderPrefixActivityResult + base64.RawURLEncoding.EncodeToString(b)

	anydata, err := anypb.New(result)
	if err != nil {
		return err
	}

	return common.CreateReminderWithRetry(ctx, f.reminders, &actorapi.CreateReminderRequest{
		ActorType: wfActorType,
		ActorID:   wfActorID,
		DueTime:   "0s",
		Name:      reminderName,
		// One shot, retry forever, jittered interval.
		FailurePolicy: common.RetryForeverPolicy(),
		Data:          anydata,
	})
}
