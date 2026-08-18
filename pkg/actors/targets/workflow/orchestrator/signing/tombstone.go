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

package signing

import (
	"context"
	"fmt"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/state"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
)

// Tombstone appends an unsigned ExecutionCompleted(FAILED) tamper marker to
// the workflow's history (see [wfenginestate.MarkAsTamperFailed]) so it
// surfaces as terminally FAILED on every subsequent load, then deletes the
// actor's reminders so no further activations fire against the dead workflow.
// The original (untrusted) history, inbox, signatures, and certs are left
// intact for forensics. Returns the new (failed) state; the caller is
// responsible for refreshing any cached views (state, rstate, ometa).
func (s *Signing) Tombstone(ctx context.Context, actorState state.Interface, opts wfenginestate.Options, prior *wfenginestate.State, cause error) (*wfenginestate.State, error) {
	log.Warnf("Workflow actor '%s': tampering detected, marking workflow as FAILED: %s", s.ActorID, cause)

	failed, err := wfenginestate.MarkAsTamperFailed(ctx, actorState, s.ActorID, opts, prior, cause)
	if err != nil {
		return nil, fmt.Errorf("failed to append tamper marker: %w", err)
	}

	s.failSignatureVerification(ctx)
	return failed, nil
}

// failSignatureVerification deletes all reminders for this workflow to
// prevent endless retries when signature verification has failed. The
// workflow state is left untouched; the tamper marker inserted by
// Tombstone is what surfaces the FAILED status on subsequent loads.
func (s *Signing) failSignatureVerification(ctx context.Context) {
	log.Warnf("Workflow actor '%s': signature verification failed, deleting reminders to stop retries", s.ActorID)

	if err := s.Reminders.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
		ActorType: s.ActorType,
		ActorID:   s.ActorID,
	}); err != nil {
		log.Errorf("Workflow actor '%s': failed to delete workflow reminders: %s", s.ActorID, err)
	}

	if err := s.Reminders.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
		ActorType:       s.ActivityActorType,
		ActorID:         s.ActorID + "::",
		MatchIDAsPrefix: true,
	}); err != nil {
		log.Errorf("Workflow actor '%s': failed to delete activity reminders: %s", s.ActorID, err)
	}
}
