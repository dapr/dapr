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

package internal

import (
	"context"
	"io"
)

// ExecuteTimerFn is the type of the function invoked when a timer is to be executed.
// If this method returns false, the timer does not repeat.
type ExecuteTimerFn func(reminder *Reminder) bool

// TimersProvider is the interface for the object that provides timers services.
type TimersProvider interface {
	io.Closer

	Init(ctx context.Context) error
	CreateTimer(ctx context.Context, reminder *Reminder) error
	DeleteTimer(ctx context.Context, timerKey string) error
	GetActiveTimersCount(actorKey string) int64

	SetExecuteTimerFn(fn ExecuteTimerFn)
}
