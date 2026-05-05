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
	"errors"
	"fmt"
	"time"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend/historysigning"
)

// VerifyAndAbsorbPropagatedHistory verifies every chunk of an inbound
// PropagatedHistory against its claimed producer-app SPIFFE identity,
// chains-of-trust to a Sentry trust anchor, and signature chain. On
// success, foreign signing certs are absorbed into
// state.ExternalSigningCertificates (ext-sigcert-NNNNNN) and the
// per-orchestrator cert-trust cache is updated so subsequent inbox
// attestations / loads from the same signers can skip chain-of-trust.
//
// Failure: returns an error and absorbs nothing. Caller must reject the
// inbound message (do not persist or run).
//
// No-op when Signer is nil.
func (s *Signing) VerifyAndAbsorbPropagatedHistory(ph *protos.PropagatedHistory, state *wfenginestate.State) error {
	if s.Signer == nil || ph == nil || len(ph.GetChunks()) == 0 {
		return nil
	}

	// Build skip set for cert digests we've already chain-verified this
	// actor lifetime. Use time.Now() as the conservative ingestion-time
	// check, mirroring VerifyInboxAttestation: the propagated payload's
	// events bear timestamps from the (possibly compromised) sender, so
	// we can't trust them for the validity check at receive time. Once
	// these events become part of our own signed history, downstream
	// re-checks use event timestamps.
	now := time.Now()
	skip := make(map[string]struct{})
	for _, chunk := range ph.GetChunks() {
		for _, der := range chunk.GetSigningCertChains() {
			digest := historysigning.CertDigest(der)
			if s.certChainTrustVerified(digest, now) {
				skip[string(digest)] = struct{}{}
			}
		}
	}

	res, err := historysigning.VerifyPropagatedHistory(historysigning.VerifyPropagationOptions{
		History:                     ph,
		Signer:                      s.Signer,
		SkipChainOfTrustCertDigests: skip,
	})
	if err != nil {
		return err
	}

	for digestStr, der := range res.VerifiedCerts {
		digest := []byte(digestStr)
		if _, addErr := state.AddExternalCert(digest, der); addErr != nil {
			return fmt.Errorf("failed to absorb verified foreign cert into ext-sigcert: %w", addErr)
		}
		s.cacheCertChainTrust(digest, der)
	}

	return nil
}

// VerifyPropagatedHistoryStateless verifies the propagated history without
// absorbing certs into a state.State. Use for stateless callers (e.g. the
// activity actor) that have no ext-sigcert table to populate. Returns an
// error on any verification failure; the caller should reject the
// invocation. No-op when Signer is nil.
func (s *Signing) VerifyPropagatedHistoryStateless(ph *protos.PropagatedHistory) error {
	if s.Signer == nil || ph == nil || len(ph.GetChunks()) == 0 {
		return nil
	}
	_, err := historysigning.VerifyPropagatedHistory(historysigning.VerifyPropagationOptions{
		History: ph,
		Signer:  s.Signer,
	})
	return err
}

// SignOutgoingPropagatedHistory attaches a fresh chunk-local signature +
// cert chain to the current-app chunk inside an outbound
// PropagatedHistory. Lineage chunks (events produced by upstream apps) are
// forwarded verbatim - they were already signed by their producer.
//
// The current-app chunk is identified by chunk.AppId == s.OwnAppID.
// Signing is a fresh single-batch signature over the chunk's events:
// receivers verify the chain self-consistently and the leaf SPIFFE
// identity against chunk.appId.
//
// No-op when Signer is nil.
func (s *Signing) SignOutgoingPropagatedHistory(ph *protos.PropagatedHistory, ownAppID string) error {
	if s.Signer == nil || ph == nil {
		return nil
	}
	for _, chunk := range ph.GetChunks() {
		if chunk.GetAppId() != ownAppID {
			// Lineage chunk from an upstream app: its signatures and
			// cert chain were attached by that app at its dispatch
			// time and travel with the chain. Forward verbatim.
			continue
		}

		start := int(chunk.GetStartEventIndex())
		count := int(chunk.GetEventCount())
		if start < 0 || count <= 0 || start+count > len(ph.GetEvents()) {
			return fmt.Errorf("own-app propagated chunk has invalid range [%d, %d) over %d events", start, start+count, len(ph.GetEvents()))
		}
		chunkEvents := ph.GetEvents()[start : start+count]

		rawEvents := make([][]byte, len(chunkEvents))
		for i, e := range chunkEvents {
			data, err := historysigning.MarshalEvent(e)
			if err != nil {
				return fmt.Errorf("failed to marshal event %d for outbound chunk: %w", start+i, err)
			}
			rawEvents[i] = data
		}

		// Single-batch fresh signature. StartEventIndex is 0 (chunk-
		// local indexing) and there is no previous signature.
		res, err := historysigning.Sign(s.Signer, historysigning.SignOptions{
			RawEvents:            rawEvents,
			StartEventIndex:      0,
			PreviousSignatureRaw: nil,
			ExistingCerts:        nil,
		})
		if err != nil {
			return fmt.Errorf("failed to sign outbound propagated chunk: %w", err)
		}
		if res.NewCert == nil {
			return errors.New("signer returned no cert for outbound propagated chunk")
		}

		certChain := append([]byte(nil), res.NewCert.GetCertificate()...)
		rawSig := append([]byte(nil), res.RawSignature...)
		if err := historysigning.BuildSignedChunk(chunk, [][]byte{rawSig}, [][]byte{certChain}); err != nil {
			return fmt.Errorf("failed to attach signatures to propagated chunk: %w", err)
		}
	}
	return nil
}
