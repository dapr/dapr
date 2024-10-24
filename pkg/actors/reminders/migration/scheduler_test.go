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
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal"
	"github.com/dapr/dapr/pkg/actors/internal/fake"
)

func Test_ToScheduler(t *testing.T) {
	reminderPeriod1, err := internal.NewReminderPeriod("PT1S")
	require.NoError(t, err)
	reminderPeriod2, err := internal.NewReminderPeriod("PT2S")
	require.NoError(t, err)

	expTime1 := time.Now().Add(time.Second)
	expTime2 := time.Now().Add(time.Minute * 2)

	tests := map[string]struct {
		inputActorTypes        []string
		stateListReminders     map[string][]*internal.Reminder
		schedulerListReminders map[string][]*internal.Reminder
		lookupActors           map[string]map[string]bool
		expCreateReminders     []*internal.CreateReminderRequest
	}{
		"no types": {},
		"no reminders": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {},
				"type2": {},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {},
				"type2": {},
			},
		},
		"not hosted": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
				},
				"type2": {
					{ActorType: "type2", ActorID: "id2", Name: "name2", DueTime: "0s"},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {},
				"type2": {},
			},
			lookupActors: map[string]map[string]bool{
				"type1": {"id1": false},
				"type2": {"id2": false},
			},
		},
		"missing": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
				},
				"type2": {
					{ActorType: "type2", ActorID: "id2", Name: "name2", DueTime: "0s"},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {},
				"type2": {},
			},
			lookupActors: map[string]map[string]bool{
				"type1": {"id1": true},
				"type2": {"id2": true},
			},
			expCreateReminders: []*internal.CreateReminderRequest{
				{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
				{ActorType: "type2", ActorID: "id2", Name: "name2", DueTime: "0s"},
			},
		},
		"missing multiple ids": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id2", Name: "name2", DueTime: "0s"},
				},
				"type2": {
					{ActorType: "type2", ActorID: "id3", Name: "name3", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id4", Name: "name4", DueTime: "0s"},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {},
				"type2": {},
			},
			lookupActors: map[string]map[string]bool{
				"type1": {"id1": true, "id2": true},
				"type2": {"id3": true, "id4": true},
			},
			expCreateReminders: []*internal.CreateReminderRequest{
				{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
				{ActorType: "type1", ActorID: "id2", Name: "name2", DueTime: "0s"},
				{ActorType: "type2", ActorID: "id3", Name: "name3", DueTime: "0s"},
				{ActorType: "type2", ActorID: "id4", Name: "name4", DueTime: "0s"},
			},
		},
		"some not hosted": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id2", Name: "name2", DueTime: "0s"},
				},
				"type2": {
					{ActorType: "type2", ActorID: "id3", Name: "name3", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id4", Name: "name4", DueTime: "0s"},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {},
				"type2": {},
			},
			lookupActors: map[string]map[string]bool{
				"type1": {"id1": true, "id2": false},
				"type2": {"id3": true, "id4": false},
			},
			expCreateReminders: []*internal.CreateReminderRequest{
				{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
				{ActorType: "type2", ActorID: "id3", Name: "name3", DueTime: "0s"},
			},
		},
		"multiple reminders per actor": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id2", Name: "name1", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id2", Name: "name2", DueTime: "0s"},
				},
				"type2": {
					{ActorType: "type2", ActorID: "id2", Name: "name3", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id2", Name: "name4", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id2", Name: "name5", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id2", Name: "name6", DueTime: "0s"},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {},
				"type2": {},
			},
			lookupActors: map[string]map[string]bool{
				"type1": {"id1": true, "id2": false},
				"type2": {"id2": true},
			},
			expCreateReminders: []*internal.CreateReminderRequest{
				{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
				{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s"},
				{ActorType: "type2", ActorID: "id2", Name: "name3", DueTime: "0s"},
				{ActorType: "type2", ActorID: "id2", Name: "name4", DueTime: "0s"},
				{ActorType: "type2", ActorID: "id2", Name: "name5", DueTime: "0s"},
				{ActorType: "type2", ActorID: "id2", Name: "name6", DueTime: "0s"},
			},
		},
		"reminders already migrated": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id2", Name: "name1", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id2", Name: "name2", DueTime: "0s"},
				},
				"type2": {
					{ActorType: "type2", ActorID: "id2", Name: "name3", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id2", Name: "name4", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id2", Name: "name5", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id2", Name: "name6", DueTime: "0s"},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id2", Name: "name1", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id2", Name: "name2", DueTime: "0s"},
				},
				"type2": {
					{ActorType: "type2", ActorID: "id2", Name: "name3", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id2", Name: "name4", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id2", Name: "name5", DueTime: "0s"},
					{ActorType: "type2", ActorID: "id2", Name: "name6", DueTime: "0s"},
				},
			},
			lookupActors: map[string]map[string]bool{
				"type1": {"id1": true, "id2": false},
				"type2": {"id2": true},
			},
		},
		"reminders exist but wrong due time": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "1s"},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "1s"},
				},
				"type2": {},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s"},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s"},
				},
				"type2": {},
			},
			lookupActors: map[string]map[string]bool{
				"type1": {"id1": true},
			},
			expCreateReminders: []*internal.CreateReminderRequest{
				{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "1s"},
				{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "1s"},
			},
		},
		"reminders exist with same data": {
			inputActorTypes: []string{"type1"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Data: json.RawMessage("data1")},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Data: json.RawMessage("data2")},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Data: json.RawMessage("data1")},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Data: json.RawMessage("data2")},
				},
			},
			lookupActors: map[string]map[string]bool{
				"type1": {"id1": true},
			},
		},
		"reminders exist but wrong data": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Data: json.RawMessage("data1")},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Data: json.RawMessage("data2")},
				},
				"type2": {},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Data: json.RawMessage("data2")},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Data: json.RawMessage("data1")},
				},
				"type2": {},
			},
			lookupActors: map[string]map[string]bool{
				"type1": {"id1": true},
				"type2": {"id2": true},
			},
			expCreateReminders: []*internal.CreateReminderRequest{
				{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Data: json.RawMessage("data1")},
				{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Data: json.RawMessage("data2")},
			},
		},
		"remiders exist with same period": {
			inputActorTypes: []string{"type1", "type2"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Period: reminderPeriod1},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Period: reminderPeriod1},
				},
				"type2": {},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Period: reminderPeriod1},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Period: reminderPeriod1},
				},
				"type2": {},
			},
			lookupActors: map[string]map[string]bool{"type1": {"id1": true}},
		},
		"reminders exist but wrong period": {
			inputActorTypes: []string{"type1"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Period: reminderPeriod1},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Period: reminderPeriod1},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Period: reminderPeriod2},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Period: reminderPeriod2},
				},
			},
			lookupActors: map[string]map[string]bool{"type1": {"id1": true}},
			expCreateReminders: []*internal.CreateReminderRequest{
				{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", Period: "PT1S"},
				{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", Period: "PT1S"},
			},
		},
		"reminders exist with same ttl": {
			inputActorTypes: []string{"type1"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", ExpirationTime: expTime1},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", ExpirationTime: expTime1},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", ExpirationTime: expTime1},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", ExpirationTime: expTime1},
				},
			},
			lookupActors:       map[string]map[string]bool{"type1": {"id1": true}},
			expCreateReminders: []*internal.CreateReminderRequest{},
		},
		"reminders exist but wrong ttl": {
			inputActorTypes: []string{"type1"},
			stateListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", ExpirationTime: expTime1},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", ExpirationTime: expTime1},
				},
			},
			schedulerListReminders: map[string][]*internal.Reminder{
				"type1": {
					{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", ExpirationTime: expTime2},
					{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", ExpirationTime: expTime2},
				},
			},
			expCreateReminders: []*internal.CreateReminderRequest{
				{ActorType: "type1", ActorID: "id1", Name: "name1", DueTime: "0s", TTL: expTime1.UTC().Format(time.RFC3339)},
				{ActorType: "type1", ActorID: "id1", Name: "name2", DueTime: "0s", TTL: expTime1.UTC().Format(time.RFC3339)},
			},
			lookupActors: map[string]map[string]bool{"type1": {"id1": true}},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stateStore := fake.NewRemindersProvider().
				WithListReminders(func(_ context.Context, req internal.ListRemindersRequest) ([]*internal.Reminder, error) {
					assert.Contains(t, test.stateListReminders, req.ActorType)
					rs, ok := test.stateListReminders[req.ActorType]
					require.True(t, ok)
					return rs, nil
				}).
				WithCreateReminder(func(context.Context, *internal.CreateReminderRequest) error {
					t.Fatalf("unexpected call to CreateReminders")
					return nil
				})

			var creates int
			t.Cleanup(func() { assert.Len(t, test.expCreateReminders, creates) })

			scheduler := fake.NewRemindersProvider().
				WithListReminders(func(_ context.Context, req internal.ListRemindersRequest) ([]*internal.Reminder, error) {
					assert.Contains(t, test.schedulerListReminders, req.ActorType)
					rs, ok := test.schedulerListReminders[req.ActorType]
					require.True(t, ok)
					return rs, nil
				}).
				WithCreateReminder(func(_ context.Context, req *internal.CreateReminderRequest) error {
					assert.Contains(t, test.expCreateReminders, req)
					creates++
					return nil
				})

			lookupFn := func(_ context.Context, actorType string, actorID string) (bool, string) {
				ids, ok := test.lookupActors[actorType]
				require.True(t, ok)
				local, ok := ids[actorID]
				require.True(t, ok)
				return local, ""
			}

			require.NoError(t, ToScheduler(context.Background(), ToSchedulerOptions{
				ActorTypes:         test.inputActorTypes,
				LookUpActorFn:      lookupFn,
				StateReminders:     stateStore,
				SchedulerReminders: scheduler,
			}))
		})
	}
}
