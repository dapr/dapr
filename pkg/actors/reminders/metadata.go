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

package reminders

import (
	"hash/fnv"
	"strconv"

	"github.com/google/uuid"

	"github.com/dapr/dapr/pkg/actors/internal"
)

// ActorMetadata represents information about the actor type.
type ActorMetadata struct {
	ID                string                 `json:"id"`
	RemindersMetadata ActorRemindersMetadata `json:"actorRemindersMetadata"`
	Etag              *string                `json:"-"`
}

// ActorRemindersMetadata represents information about actor's reminders.
type ActorRemindersMetadata struct {
	PartitionCount int                `json:"partitionCount"`
	PartitionsEtag map[uint32]*string `json:"-"`
}

type ActorReminderReference struct {
	ActorMetadataID           string
	ActorRemindersPartitionID uint32
	Reminder                  internal.Reminder
}

func (m *ActorMetadata) calculateReminderPartition(actorID, reminderName string) uint32 {
	if m.RemindersMetadata.PartitionCount <= 0 {
		return 0
	}

	// do not change this hash function because it would be a breaking change.
	h := fnv.New32a()
	h.Write([]byte(actorID))
	h.Write([]byte(reminderName))
	return (h.Sum32() % uint32(m.RemindersMetadata.PartitionCount)) + 1
}

func (m *ActorMetadata) createReminderReference(reminder internal.Reminder) ActorReminderReference {
	if m.RemindersMetadata.PartitionCount > 0 {
		return ActorReminderReference{
			ActorMetadataID:           m.ID,
			ActorRemindersPartitionID: m.calculateReminderPartition(reminder.ActorID, reminder.Name),
			Reminder:                  reminder,
		}
	}

	return ActorReminderReference{
		ActorMetadataID:           uuid.Nil.String(),
		ActorRemindersPartitionID: 0,
		Reminder:                  reminder,
	}
}

func (m *ActorMetadata) calculateRemindersStateKey(actorType string, remindersPartitionID uint32) string {
	if remindersPartitionID == 0 {
		return constructCompositeKey("actors", actorType)
	}

	return constructCompositeKey(
		"actors",
		actorType,
		m.ID,
		"reminders",
		strconv.Itoa(int(remindersPartitionID)))
}

func (m *ActorMetadata) calculateEtag(partitionID uint32) *string {
	return m.RemindersMetadata.PartitionsEtag[partitionID]
}

func (m *ActorMetadata) removeReminderFromPartition(reminderRefs []ActorReminderReference, actorType, actorID, reminderName string) (bool, []internal.Reminder, string, *string) {
	// First, we find the partition
	var partitionID uint32
	l := len(reminderRefs)
	if m.RemindersMetadata.PartitionCount > 0 {
		var found bool
		for _, reminderRef := range reminderRefs {
			if reminderRef.Reminder.ActorType == actorType && reminderRef.Reminder.ActorID == actorID && reminderRef.Reminder.Name == reminderName {
				partitionID = reminderRef.ActorRemindersPartitionID
				found = true
				break
			}
		}

		// If the reminder doesn't exist, return without making any change
		if !found {
			return false, nil, "", nil
		}

		// When calculating the initial allocated size of remindersInPartitionAfterRemoval, if we have partitions assume len(reminderRefs)/PartitionCount for an initial count
		// This is unlikely to avoid all re-allocations, but it's still better than allocating the slice with capacity 0
		l /= m.RemindersMetadata.PartitionCount
	}

	remindersInPartitionAfterRemoval := make([]internal.Reminder, 0, l)
	var found bool
	for _, reminderRef := range reminderRefs {
		if reminderRef.Reminder.ActorType == actorType && reminderRef.Reminder.ActorID == actorID && reminderRef.Reminder.Name == reminderName {
			found = true
			continue
		}

		// Only the items in the partition to be updated.
		if reminderRef.ActorRemindersPartitionID == partitionID {
			remindersInPartitionAfterRemoval = append(remindersInPartitionAfterRemoval, reminderRef.Reminder)
		}
	}

	// If no reminder found, return false here to short-circuit the next operations
	if !found {
		return false, nil, "", nil
	}

	stateKey := m.calculateRemindersStateKey(actorType, partitionID)
	return true, remindersInPartitionAfterRemoval, stateKey, m.calculateEtag(partitionID)
}

func (m *ActorMetadata) insertReminderInPartition(reminderRefs []ActorReminderReference, reminder internal.Reminder) ([]internal.Reminder, ActorReminderReference, string, *string) {
	newReminderRef := m.createReminderReference(reminder)

	var remindersInPartitionAfterInsertion []internal.Reminder
	for _, reminderRef := range reminderRefs {
		// Only the items in the partition to be updated.
		if reminderRef.ActorRemindersPartitionID == newReminderRef.ActorRemindersPartitionID {
			remindersInPartitionAfterInsertion = append(remindersInPartitionAfterInsertion, reminderRef.Reminder)
		}
	}

	remindersInPartitionAfterInsertion = append(remindersInPartitionAfterInsertion, reminder)

	stateKey := m.calculateRemindersStateKey(newReminderRef.Reminder.ActorType, newReminderRef.ActorRemindersPartitionID)
	return remindersInPartitionAfterInsertion, newReminderRef, stateKey, m.calculateEtag(newReminderRef.ActorRemindersPartitionID)
}

func (m *ActorMetadata) calculateDatabasePartitionKey(stateKey string) string {
	if m.RemindersMetadata.PartitionCount > 0 {
		return m.ID
	}

	return stateKey
}
