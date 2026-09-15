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
// backoff. Every error is retried except a context error, a clearly-permanent
// request error (see IsPermanentCreateError), or scheduler storage exhaustion
// (ResourceExhausted). The create is often the only signal that will ever
// advance durable state committed just before it (a workflow's start, timer,
// or activity reminder), so dropping an unrecognised-but-transient error
// strands that state forever: the scheduler surfaces its shutdown errors
// ("cron is closed") as plain errors that cross the wire as Unknown, and no
// allowlist of codes can anticipate every such case. A genuinely permanent
// error outside the denylist costs at most the bounded budget before
// surfacing.
//
// ResourceExhausted is permanent here, unlike in
// CreateReminderWithRetryForever: etcd surfaces a full storage quota
// (NOSPACE) as ResourceExhausted, which only clears with compaction or
// operator intervention, and this bounded create sits on a synchronous caller
// path where burning the budget replaces the actionable "database space
// exceeded" with the caller's own deadline error.
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
		if err != nil && (IsPermanentCreateError(err) || status.Code(err) == codes.ResourceExhausted) {
			return backoff.Permanent(err)
		}
		return err
	}, backoff.WithContext(bo, ctx))
}

// CreateReminderWithRetryForever calls reminders.Create and retries on every
// error except a context error or a clearly-permanent request error (see
// IsPermanentCreateError), with no overall time bound: it stops only when ctx
// is cancelled (i.e. the actor is torn down).
func CreateReminderWithRetryForever(ctx context.Context, r reminderCreator, req *actorapi.CreateReminderRequest) error {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond
	bo.MaxInterval = 5 * time.Second
	bo.MaxElapsedTime = 0 // retry until ctx is cancelled.

	return backoff.Retry(func() error {
		if err := ctx.Err(); err != nil {
			return backoff.Permanent(err)
		}
		err := r.Create(ctx, req)
		if err != nil && IsPermanentCreateError(err) {
			return backoff.Permanent(err)
		}
		return err
	}, backoff.WithContext(bo, ctx))
}

// IsPermanentCreateError reports whether a reminder Create error is a
// client-side mistake that retrying can never fix (malformed request, missing
// auth, unimplemented method). A context error is not a gRPC status and so
// reads as permanent here. Everything else: Unavailable, DeadlineExceeded,
// Internal, Aborted, and notably Unknown (the code carried by the scheduler's
// plain-error returns, including cron shutdown), is treated as retryable by
// both retry helpers, because the scheduler may recover and the create is an
// idempotent overwrite-by-name. ResourceExhausted (transient etcd pressure)
// is also retryable for the forever helper; the bounded helper additionally
// treats it as permanent, see CreateReminderWithRetry.
func IsPermanentCreateError(err error) bool {
	s, ok := status.FromError(err)
	if !ok {
		// Not a gRPC status (e.g. a local marshalling error): retrying forever
		// will not help.
		return true
	}
	switch s.Code() {
	case codes.InvalidArgument, codes.PermissionDenied, codes.Unauthenticated,
		codes.FailedPrecondition, codes.Unimplemented, codes.NotFound:
		return true
	default:
		return false
	}
}
