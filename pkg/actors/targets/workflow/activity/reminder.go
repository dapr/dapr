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

	"google.golang.org/protobuf/types/known/anypb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/durabletask-go/backend"
)

func (a *activity) createReminder(ctx context.Context, his *backend.HistoryEvent) error {
	const reminderName = "run-activity"
	log.Debugf("Activity actor '%s||%s': creating reminder '%s' for immediate execution", a.actorType, a.actorID, reminderName)

	var period string
	var oneshot bool
	if a.schedulerReminders {
		oneshot = true
	} else {
		period = a.reminderInterval.String()
	}

	anydata, err := anypb.New(his)
	if err != nil {
		return err
	}

	return a.reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: a.actorType,
		ActorID:   a.actorID,
		DueTime:   "0s",
		Name:      reminderName,
		Period:    period,
		IsOneShot: oneshot,
		Data:      anydata,
	})
}
