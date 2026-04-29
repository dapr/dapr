/*
Copyright 2026 The Dapr Authors
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

package common

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
)

// reminderCreator is the minimal interface needed by CreateReminderWithRetry.
// It is satisfied by both reminders.Interface (used by orchestrator) and
// scheduler.Interface (used by activity).
type reminderCreator interface {
	Create(ctx context.Context, req *actorapi.CreateReminderRequest) error
}

// reminderCreateMaxElapsedTime bounds how long a single reminder Create will
// be retried in-process before giving up.
const reminderCreateMaxElapsedTime = time.Minute

// CreateReminderWithRetry calls reminders.Create with bounded exponential
// backoff.
func CreateReminderWithRetry(ctx context.Context, r reminderCreator, req *actorapi.CreateReminderRequest) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	bo.MaxInterval = 5 * time.Second
	bo.MaxElapsedTime = reminderCreateMaxElapsedTime

	return backoff.Retry(func() error {
		if err := ctx.Err(); err != nil {
			return backoff.Permanent(err)
		}
		return r.Create(ctx, req)
	}, backoff.WithContext(bo, ctx))
}
