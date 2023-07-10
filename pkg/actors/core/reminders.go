package core

import (
	"context"
	"time"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors/core/reminder"
)

//nolint:interfacebloat
type Reminders interface {
	GetReminder(ctx context.Context, req *reminder.GetReminderRequest) (*Reminder, error)
	CreateReminder(ctx context.Context, req *reminder.CreateReminderRequest) error
	DeleteReminder(ctx context.Context, req *reminder.DeleteReminderRequest) error
	RenameReminder(ctx context.Context, req *reminder.RenameReminderRequest) error
	SetStateStore(store TransactionalStateStore)
	ExecuteStateStoreTransaction(ctx context.Context, store TransactionalStateStore, operations []state.TransactionalStateOperation, metadata map[string]string) error
	StartReminder(reminder *Reminder, stopChannel chan struct{}) error
	ExecuteReminder(reminder *Reminder, isTimer bool) (err error)
	GetActorTypeMetadata(ctx context.Context, actorType string, migrate bool) (*ActorMetadata, error)
	GetReminderTrack(ctx context.Context, key string) (*reminder.ReminderTrack, error)
	UpdateReminderTrack(ctx context.Context, key string, repetition int, lastInvokeTime time.Time, etag *string) error
}
