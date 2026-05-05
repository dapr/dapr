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
	"fmt"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/backend/historysigning"
)

// SignNewEvents deterministically marshals the newly added history events,
// creates a HistorySignature covering them, and appends it (along with any
// new signing certificate) to the state. The marshaled bytes are stored on
// the state so that GetSaveRequest can persist the exact bytes that were
// signed. No-op if Signer is nil.
func (s *Signing) SignNewEvents(state *wfenginestate.State) error {
	if s.Signer == nil {
		return nil
	}

	newEventCount := state.HistoryAddedCount()
	if newEventCount == 0 {
		return nil
	}

	if newEventCount > len(state.History) {
		return fmt.Errorf("SignNewEvents called with newEventCount=%d but history has only %d events",
			newEventCount, len(state.History))
	}

	//nolint:gosec
	startIndex := uint64(len(state.History) - newEventCount)

	// When there is no previous signature, this must be the very first
	// signature for the workflow, which requires the batch to cover the
	// entire history. Otherwise we'd produce a chain-less signature over
	// a suffix of the history which the verifier would reject, but the
	// defect would only surface on reload.
	if len(state.RawSignatures) == 0 && startIndex != 0 {
		return fmt.Errorf("cannot sign partial history (%d events, starting at index %d) without a previous signature",
			newEventCount, startIndex)
	}

	rawNewEvents := make([][]byte, newEventCount)
	for i := range newEventCount {
		data, err := historysigning.MarshalEvent(state.History[startIndex+uint64(i)])
		if err != nil {
			return fmt.Errorf("failed to marshal history event %d: %w", startIndex+uint64(i), err)
		}
		rawNewEvents[i] = data
	}

	state.SetMarshaledNewHistory(rawNewEvents)

	var prevSigRaw []byte
	if len(state.RawSignatures) > 0 {
		prevSigRaw = state.RawSignatures[len(state.RawSignatures)-1]
	}

	result, err := historysigning.Sign(s.Signer, historysigning.SignOptions{
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
