package core

import (
	"context"

	"github.com/dapr/dapr/pkg/actors/core/reminder"
)

type Timers interface {
	CreateTimer(ctx context.Context, req *reminder.CreateTimerRequest) error
	DeleteTimer(ctx context.Context, req *reminder.DeleteTimerRequest) error
}
