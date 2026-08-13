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

package state

import (
	"context"
	"errors"
	"time"

	"github.com/dapr/dapr/pkg/actors/state"
)

const (
	// loadStateMaxAttempts and loadStateRetryDelay bound the reload retries when
	// a load observes metadata and entry rows from different committed snapshots
	// (a save landing between the metadata Get and the bulk Get).
	loadStateMaxAttempts = 5
	loadStateRetryDelay  = 15 * time.Millisecond
)

// LoadWorkflowState loads the workflow state from the actor state store. The
// metadata row and the inbox/history entry rows are read in two separate
// state-store calls, so a concurrent actor save committing between them can
// delete entry rows the just-read metadata still declares. That torn read is
// transient: retry the whole load with fresh metadata. If the metadata ETag is
// unchanged between two attempts no save landed in between, so the data is
// genuinely missing and the error is surfaced.
func LoadWorkflowState(ctx context.Context, astate state.Interface, actorID string, opts Options) (*State, error) {
	var prevETag *string
	var havePrev bool
	for attempt := 1; ; attempt++ {
		s, err := loadWorkflowStateOnce(ctx, astate, actorID, opts)
		var mErr *transientKeyMismatchError
		if err == nil || !errors.As(err, &mErr) {
			return s, err
		}
		if havePrev && prevETag != nil && mErr.etag != nil && *prevETag == *mErr.etag {
			return nil, mErr.err
		}
		if attempt >= loadStateMaxAttempts {
			return nil, mErr.err
		}
		prevETag, havePrev = mErr.etag, true
		wfLogger.Debug("workflow load raced a concurrent save, retrying",
			"actor_id", actorID, "attempt", attempt, "max_attempts", loadStateMaxAttempts, "error", mErr.err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(loadStateRetryDelay):
		}
	}
}

// transientKeyMismatchError marks a load that observed metadata declaring an
// inbox or history key which the bulk read did not return. It carries the
// metadata ETag of the failed attempt so the retry loop can distinguish a
// concurrent save (metadata changed) from genuinely missing data.
type transientKeyMismatchError struct {
	err  error
	etag *string
}

func (e *transientKeyMismatchError) Error() string { return e.err.Error() }
func (e *transientKeyMismatchError) Unwrap() error { return e.err }
