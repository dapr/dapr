package core

import (
	"context"
	"hash/fnv"
	"strconv"

	"github.com/dapr/components-contrib/state"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// Actors allow calling into virtual actors as well as actor state management.
//
//nolint:interfacebloat
type Actors interface {
	Call(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error)
	Init() error
	Stop()
	GetState(ctx context.Context, req *GetStateRequest) (*StateResponse, error)
	TransactionalStateOperation(ctx context.Context, req *TransactionalRequest) error
	CreateTimer(ctx context.Context, req *CreateTimerRequest) error
	DeleteTimer(ctx context.Context, req *DeleteTimerRequest) error
	IsActorHosted(ctx context.Context, req *ActorHostedRequest) bool
	GetActiveActorsCount(ctx context.Context) []*runtimev1pb.ActiveActorsCount
	RegisterInternalActor(ctx context.Context, actorType string, actor InternalActor) error
	GetActorsReminders() Reminders
}

// ActiveActorsCount contain actorType and count of actors each type has.
type ActiveActorsCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type ActorReminderReference struct {
	ActorMetadataID           string
	ActorRemindersPartitionID uint32
	Reminder                  Reminder
}

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

type TransactionalStateStore interface {
	state.Store
	state.TransactionalStore
}

func (m *ActorMetadata) CalculateReminderPartition(actorID, reminderName string) uint32 {
	if m.RemindersMetadata.PartitionCount <= 0 {
		return 0
	}

	// do not change this hash function because it would be a breaking change.
	h := fnv.New32a()
	h.Write([]byte(actorID))
	h.Write([]byte(reminderName))
	return (h.Sum32() % uint32(m.RemindersMetadata.PartitionCount)) + 1
}

func (m *ActorMetadata) CreateReminderReference(reminder Reminder) ActorReminderReference {
	if m.RemindersMetadata.PartitionCount > 0 {
		return ActorReminderReference{
			ActorMetadataID:           m.ID,
			ActorRemindersPartitionID: m.CalculateReminderPartition(reminder.ActorID, reminder.Name),
			Reminder:                  reminder,
		}
	}

	return ActorReminderReference{
		ActorMetadataID:           metadataZeroID,
		ActorRemindersPartitionID: 0,
		Reminder:                  reminder,
	}
}

func (m *ActorMetadata) CalculateRemindersStateKey(actorType string, remindersPartitionID uint32) string {
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

func (m *ActorMetadata) CalculateEtag(partitionID uint32) *string {
	return m.RemindersMetadata.PartitionsEtag[partitionID]
}

func (m *ActorMetadata) RemoveReminderFromPartition(reminderRefs []ActorReminderReference, actorType, actorID, reminderName string) (bool, []Reminder, string, *string) {
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

	remindersInPartitionAfterRemoval := make([]Reminder, 0, l)
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

	stateKey := m.CalculateRemindersStateKey(actorType, partitionID)
	return true, remindersInPartitionAfterRemoval, stateKey, m.CalculateEtag(partitionID)
}

func (m *ActorMetadata) InsertReminderInPartition(reminderRefs []ActorReminderReference, reminder Reminder) ([]Reminder, ActorReminderReference, string, *string) {
	newReminderRef := m.CreateReminderReference(reminder)

	var remindersInPartitionAfterInsertion []Reminder
	for _, reminderRef := range reminderRefs {
		// Only the items in the partition to be updated.
		if reminderRef.ActorRemindersPartitionID == newReminderRef.ActorRemindersPartitionID {
			remindersInPartitionAfterInsertion = append(remindersInPartitionAfterInsertion, reminderRef.Reminder)
		}
	}

	remindersInPartitionAfterInsertion = append(remindersInPartitionAfterInsertion, reminder)

	stateKey := m.CalculateRemindersStateKey(newReminderRef.Reminder.ActorType, newReminderRef.ActorRemindersPartitionID)
	return remindersInPartitionAfterInsertion, newReminderRef, stateKey, m.CalculateEtag(newReminderRef.ActorRemindersPartitionID)
}

func (m *ActorMetadata) CalculateDatabasePartitionKey(stateKey string) string {
	if m.RemindersMetadata.PartitionCount > 0 {
		return m.ID
	}

	return stateKey
}
