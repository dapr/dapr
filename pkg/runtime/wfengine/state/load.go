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
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/durabletask-go/backend"
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
// genuinely missing and the error is surfaced, unless opts.AllowMissingEntries
// is set, in which case the state is loaded without the missing entries.
func LoadWorkflowState(ctx context.Context, astate state.Interface, actorID string, opts Options) (*State, error) {
	// Always attempt strict loads first so that a torn read is retried with
	// fresh metadata rather than tolerated.
	strict := opts
	strict.AllowMissingEntries = false

	var prevETag *string
	var havePrev bool
	for attempt := 1; ; attempt++ {
		s, err := loadWorkflowStateOnce(ctx, astate, actorID, strict)
		var mErr *transientKeyMismatchError
		if err == nil || !errors.As(err, &mErr) {
			if s != nil {
				s.allowMissingEntries = opts.AllowMissingEntries
			}
			return s, err
		}
		unchanged := havePrev && prevETag != nil && mErr.etag != nil && *prevETag == *mErr.etag
		if unchanged || attempt >= loadStateMaxAttempts {
			if opts.AllowMissingEntries {
				return loadWorkflowStateOnce(ctx, astate, actorID, opts)
			}
			if unchanged {
				return nil, &missingEntryError{mErr.err}
			}
			return nil, mErr.err
		}
		prevETag, havePrev = mErr.etag, true
		wfLogger.Debugf("workflow '%s': load raced a concurrent save (attempt %d/%d), retrying: %s",
			actorID, attempt, loadStateMaxAttempts, mErr.err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(loadStateRetryDelay):
		}
	}
}

// LoadSummary loads the full state of an instance, as LoadWorkflowState does,
// and returns the summary that the load derives from its history and inbox.
// LoadSummary never writes.
//
// It returns a nil summary and a nil error when the list must skip the
// instance. That is the case when the instance has no state: it does not
// exist, or it was deleted or purged. It is also the case when the state
// cannot be read for a reason that a retry cannot change. A warning is
// logged for these reasons:
//   - A row does not decode.
//   - The state fails verification (VerificationError). The list does not
//     show such an instance, with or without a state from the load.
//     GetInstance returns the error for it.
//   - A row that the metadata declares is missing, and the metadata did not
//     change between two reads.
//
// Every other load error is returned. A ConfigurationError means that this
// daprd cannot verify the signed history of the instance. A failed store
// read can succeed on a retry.
func LoadSummary(ctx context.Context, astate state.Interface, actorID string, opts Options) (*backend.BackendWorkflowSummary, error) {
	s, err := LoadWorkflowState(ctx, astate, actorID, opts)
	if err != nil {
		var verr *wferrors.VerificationError
		var derr *decodeError
		var merr *missingEntryError
		if errors.As(err, &verr) || errors.As(err, &derr) || errors.As(err, &merr) {
			wfLogger.Warnf("Workflow '%s': the list skips the instance because its state cannot be read: %v", actorID, err)
			return nil, nil
		}
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	return s.Summary(), nil
}

// missingEntryError marks a load that found a row missing which the metadata
// declares, with the same metadata ETag on two reads. No save landed between
// the reads, so a retry gives the same result. The message and the wrapped
// error do not change.
type missingEntryError struct{ err error }

func (e *missingEntryError) Error() string { return e.err.Error() }
func (e *missingEntryError) Unwrap() error { return e.err }

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
