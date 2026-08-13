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

func (a *activity) createReminder(ctx context.Context, invocation *protos.ActivityInvocation, dueTime time.Time, activityName *string) error {
	const reminderName = "run-activity"
	log.Debug("Activity actor ||: creating reminder with dueTime=", "actor_type", a.actorType, "actor_id", a.actorID, "reminder", reminderName, "due_time", dueTime)

	anydata, err := anypb.New(invocation)
	if err != nil {
		return err
	}

	// The activity actor should always create reminders for its own actor type
	// and ID
	return common.CreateReminderWithRetry(ctx, a.reminders, &actorapi.CreateReminderRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		DueTime:   dueTime.Format(time.RFC3339Nano),
		Name:      reminderName,
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
