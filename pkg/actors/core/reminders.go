package core

import (
	"context"
	"time"

	"github.com/dapr/components-contrib/state"
)

type Reminders interface {
	GetReminder(ctx context.Context, req *GetReminderRequest) (*Reminder, error)
	CreateReminder(ctx context.Context, req *CreateReminderRequest) error
	DeleteReminder(ctx context.Context, req *DeleteReminderRequest) error
	RenameReminder(ctx context.Context, req *RenameReminderRequest) error
	SetStateStore(store TransactionalStateStore)
	ExecuteStateStoreTransaction(ctx context.Context, store TransactionalStateStore, operations []state.TransactionalStateOperation, metadata map[string]string) error
	StartReminder(reminder *Reminder, stopChannel chan struct{}) error
	ExecuteReminder(reminder *Reminder, isTimer bool) (err error)
	GetActorTypeMetadata(ctx context.Context, actorType string, migrate bool) (*ActorMetadata, error)
	GetReminderTrack(ctx context.Context, key string) (*ReminderTrack, error)
	UpdateReminderTrack(ctx context.Context, key string, repetition int, lastInvokeTime time.Time, etag *string) error
}
