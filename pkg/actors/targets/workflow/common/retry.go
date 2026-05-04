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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
// backoff. Only transient gRPC errors are retried (e.g. scheduler pod
// failover surfacing as Unavailable); permanent errors such as etcd
// "database space exceeded" (ResourceExhausted) or invalid arguments are
// returned to the caller immediately. Without that distinction a permanent
// failure would burn the entire retry budget before surfacing the original
// error, masking the real cause and tripping caller deadlines.
func CreateReminderWithRetry(ctx context.Context, r reminderCreator, req *actorapi.CreateReminderRequest) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	bo.MaxInterval = 5 * time.Second
	bo.MaxElapsedTime = reminderCreateMaxElapsedTime

	return backoff.Retry(func() error {
		if err := ctx.Err(); err != nil {
			return backoff.Permanent(err)
		}
		err := r.Create(ctx, req)
		if err != nil && !isTransientCreateError(err) {
			return backoff.Permanent(err)
		}
		return err
	}, backoff.WithContext(bo, ctx))
}

// isTransientCreateError reports whether a reminder Create error should be
// retried. The retry exists to mask short scheduler-pod failovers, where
// the gRPC client surfaces Unavailable (connection lost) or DeadlineExceeded
// (per-call timeout) for the brief window before a new pod accepts the
// connection. Anything outside that allowlist (ResourceExhausted, invalid
// argument, permission denied, etc.) is a server-side decision that
// retrying will not change, so we surface it immediately.
func isTransientCreateError(err error) bool {
	s, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch s.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}
