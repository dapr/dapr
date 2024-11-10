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

package migration

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage"
	"github.com/dapr/dapr/pkg/actors/requestresponse"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.reminders.migration")

type ToSchedulerOptions struct {
	Clock clock.Clock

	Table              table.Interface
	StateReminders     storage.Interface
	SchedulerReminders storage.Interface
	LookupFn           func(context.Context, *requestresponse.LookupActorRequest) bool
}

func ToScheduler(ctx context.Context, opts ToSchedulerOptions) error {
	log.Infof("Running actor reminder migration from state store to scheduler")

	stateReminders := make(map[string][]*requestresponse.Reminder)
	schedulerReminders := make(map[string][]*requestresponse.Reminder)

	actorTypes := opts.Table.Types()

	for _, actorType := range actorTypes {
		log.Debugf("Listing state reminders for actor type %s", actorType)
		stateR, err := opts.StateReminders.List(ctx, &requestresponse.ListRemindersRequest{
			ActorType: actorType,
		})
		if err != nil {
			return err
		}
		for i := range stateR {
			if opts.LookupFn(ctx, &requestresponse.LookupActorRequest{
				ActorType: stateR[i].ActorType,
				ActorID:   stateR[i].ActorID,
			}) {
				log.Debugf("Hosted state reminder %s for actor %s in state store", stateR[i].Key(), stateR[i].ActorID)
				stateReminders[actorType] = append(stateReminders[actorType], stateR[i])
			}
		}

		log.Debugf("Listing scheduler reminders for actor type %s", actorType)
		schedR, err := opts.SchedulerReminders.List(ctx, &requestresponse.ListRemindersRequest{
			ActorType: actorType,
		})
		if err != nil {
			return err
		}
		schedulerReminders[actorType] = schedR
	}

	var missingReminders []*requestresponse.Reminder
	for _, actorType := range actorTypes {
		for _, stateReminder := range stateReminders[actorType] {
			var exists bool
			for _, schedulerReminder := range schedulerReminders[actorType] {
				if stateReminder.ActorID == schedulerReminder.ActorID &&
					stateReminder.Name == schedulerReminder.Name {
					exists = stateReminder.DueTime == schedulerReminder.DueTime &&
						stateReminder.Period.String() == schedulerReminder.Period.String() &&
						bytes.Equal(stateReminder.Data, schedulerReminder.Data) &&
						math.Abs(float64(stateReminder.ExpirationTime.Sub(schedulerReminder.ExpirationTime))) < float64(time.Minute)

					break
				}
			}

			if !exists {
				log.Debugf("Found missing scheduler reminder %s", stateReminder.Key())
				missingReminders = append(missingReminders, stateReminder)
			}
		}
	}

	if len(missingReminders) == 0 {
		log.Infof("Skipping migration, no missing scheduler reminders found")
	}

	log.Infof("Found %d missing scheduler reminders from state store", len(missingReminders))
	for _, missing := range missingReminders {
		log.Infof("Creating missing scheduler reminder %s", missing.Key())

		var ttl string
		if !missing.ExpirationTime.IsZero() {
			ttl = missing.ExpirationTime.UTC().Format(time.RFC3339)
		}

		err := opts.SchedulerReminders.Create(ctx, &requestresponse.CreateReminderRequest{
			Name:      missing.Name,
			ActorType: missing.ActorType,
			ActorID:   missing.ActorID,
			Data:      missing.Data,
			DueTime:   missing.DueTime,
			Period:    missing.Period.String(),
			TTL:       ttl,
		})
		if err != nil {
			return fmt.Errorf("failed to migrate reminder %s: %w", missing.Key(), err)
		}
	}

	log.Infof("Migrated %d reminders from state store to scheduler successfully", len(missingReminders))

	return nil
}
