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

package orchestrator

import (
	"context"
	"fmt"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/backend/historysigning"
)

// signNewEvents deterministically marshals the newly added history events,
// creates a HistorySignature covering them, and appends it (along with any
// new signing certificate) to the state.
// The marshaled bytes are stored on the state so that GetSaveRequest can
// persist the exact bytes that were signed.
// If signer is nil (mTLS disabled or feature flag off), this is a no-op.
//
// When unsigned history events exist before the new batch (e.g. the workflow
// previously ran on a non-signing host), a catch-up signature is created first
// to cover the gap, ensuring contiguous signature coverage from index 0.
func (o *orchestrator) signNewEvents(state *wfenginestate.State, newEventCount int) error {
	if o.signer == nil {
		return nil
	}

	if newEventCount == 0 {
		return nil
	}

	//nolint:gosec
	startIndex := uint64(len(state.History) - newEventCount)

	// Marshal new events deterministically. These exact bytes will be both
	// signed and persisted to the state store.
	rawNewEvents := make([][]byte, newEventCount)
	for i := range newEventCount {
		data, err := historysigning.MarshalEvent(state.History[startIndex+uint64(i)])
		if err != nil {
			return fmt.Errorf("failed to marshal history event %d: %w", startIndex+uint64(i), err)
		}
		rawNewEvents[i] = data
	}

	state.SetMarshaledNewHistory(rawNewEvents)

	// Determine where existing signatures cover up to.
	var signedUpTo uint64
	if len(state.Signatures) > 0 {
		last := state.Signatures[len(state.Signatures)-1]
		signedUpTo = last.GetStartEventIndex() + last.GetEventCount()
	}

	// If there are unsigned events before the new batch, create a catch-up
	// signature to fill the gap. This happens when a workflow transitions
	// from a non-signing host to a signing host.
	if signedUpTo < startIndex {
		if err := o.signCatchUp(state, signedUpTo, startIndex); err != nil {
			return err
		}
	}

	var prevSigRaw []byte
	if len(state.RawSignatures) > 0 {
		prevSigRaw = state.RawSignatures[len(state.RawSignatures)-1]
	}

	result, err := historysigning.Sign(o.signer, historysigning.SignOptions{
		RawEvents:            rawNewEvents,
		StartEventIndex:      startIndex,
		PreviousSignatureRaw: prevSigRaw,
		ExistingCerts:        state.SigningCertificates,
	})
	if err != nil {
		return fmt.Errorf("failed to sign history events: %w", err)
	}

	if result.NewCert != nil {
		state.AddSigningCertificate(result.NewCert)
	}

	state.AddSignature(result.Signature, result.RawSignature)

	return nil
}

// signCatchUp creates a signature covering previously unsigned history events
// in the range [signedUpTo, startIndex). It uses the raw bytes loaded from
// the state store (state.RawHistory) so the signature matches the persisted
// data.
func (o *orchestrator) signCatchUp(state *wfenginestate.State, signedUpTo, startIndex uint64) error {
	if uint64(len(state.RawHistory)) < startIndex {
		return fmt.Errorf("raw history has %d entries but need %d for catch-up signing", len(state.RawHistory), startIndex)
	}

	rawCatchUp := state.RawHistory[signedUpTo:startIndex]

	var prevSigRaw []byte
	if len(state.RawSignatures) > 0 {
		prevSigRaw = state.RawSignatures[len(state.RawSignatures)-1]
	}

	result, err := historysigning.Sign(o.signer, historysigning.SignOptions{
		RawEvents:            rawCatchUp,
		StartEventIndex:      signedUpTo,
		PreviousSignatureRaw: prevSigRaw,
		ExistingCerts:        state.SigningCertificates,
	})
	if err != nil {
		return fmt.Errorf("failed to sign catch-up history events [%d, %d): %w", signedUpTo, startIndex, err)
	}

	if result.NewCert != nil {
		state.AddSigningCertificate(result.NewCert)
	}

	state.AddSignature(result.Signature, result.RawSignature)

	return nil
}

// failSignatureVerification deletes all reminders for this workflow to prevent
// endless retries. The workflow state is left untouched.
func (o *orchestrator) failSignatureVerification(ctx context.Context) {
	log.Warnf("Workflow actor '%s': signature verification failed, deleting reminders to stop retries", o.actorID)

	if err := o.reminders.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
		ActorType: o.actorType,
		ActorID:   o.actorID,
	}); err != nil {
		log.Errorf("Workflow actor '%s': failed to delete workflow reminders: %s", o.actorID, err)
	}

	if err := o.reminders.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
		ActorType:       o.activityActorType,
		ActorID:         o.actorID + "::",
		MatchIDAsPrefix: true,
	}); err != nil {
		log.Errorf("Workflow actor '%s': failed to delete activity reminders: %s", o.actorID, err)
	}
}
