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
// When ph is nil, returns nil (nothing to verify). When ph is non-nil but
// Signer is nil, the receiver has signing disabled so this is a no-op
// accept; if any chunk carries signing material, a warning is logged so
// operators notice that signed payloads from a peer are flowing through an
// unverified receiver. Each chunk otherwise owns its own rawEvents and
// signing material; verification rejects mismatches such as empty
// rawEvents, missing signatures, or signatures that do not chain over the
// chunk's rawEvents.
func (s *Signing) VerifyAndAbsorbPropagatedHistory(ph *protos.PropagatedHistory, state *wfenginestate.State) error {
	if ph == nil {
		return nil
	}
	if s.Signer == nil {
		warnIfSigned(ph, "VerifyAndAbsorbPropagatedHistory")
		return nil
	}

	res, err := historysigning.VerifyPropagatedHistory(historysigning.VerifyPropagationOptions{
		History:           ph,
		Signer:            s.Signer,
		ExpectedNamespace: s.Namespace,
	})
	if err != nil {
		return err
	}

	// Absorb verified foreign certs. AddExternalCert is idempotent on
	// digest, so re-absorbing a cert this orchestrator has already seen
	// is a no-op write-side, and updating the per-orchestrator
	// chain-of-trust cache is cheap enough to do unconditionally.
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
// invocation.
//
// When ph is nil, returns nil. When ph is non-nil but Signer is nil, the
// receiver has signing disabled so verification is skipped; if any chunk
// carries signing material, a warning is logged so operators can detect
// that signed peer payloads are flowing through an unverified receiver.
func (s *Signing) VerifyPropagatedHistoryStateless(ph *protos.PropagatedHistory) error {
	if ph == nil {
		return nil
	}
	if s.Signer == nil {
		warnIfSigned(ph, "VerifyPropagatedHistoryStateless")
		return nil
	}
	_, err := historysigning.VerifyPropagatedHistory(historysigning.VerifyPropagationOptions{
		History:           ph,
		Signer:            s.Signer,
		ExpectedNamespace: s.Namespace,
	})
	return err
}

// warnIfSigned logs a warning if the propagated history carries any
// signing material. This catches the mixed-environment case where a
// signing-enabled producer sent a signed payload to a signing-disabled
// receiver - the receiver cannot verify the signatures, but should
// surface the discrepancy so operators notice that signing is
// inconsistently configured across the deployment.
func warnIfSigned(ph *protos.PropagatedHistory, caller string) {
	for i, chunk := range ph.GetChunks() {
		if len(chunk.GetRawSignatures()) > 0 || len(chunk.GetSigningCertChains()) > 0 {
			log.Warnf("%s: receiver has signing disabled but inbound propagated history chunk %d (app %q) carries %d signatures and %d cert chains; payload accepted unverified",
				caller, i, chunk.GetAppId(), len(chunk.GetRawSignatures()), len(chunk.GetSigningCertChains()))
			return
		}
	}
}

// SignOutgoingPropagatedHistory attaches a fresh chunk-local signature +
// cert chain to the current-app chunk inside an outbound
// PropagatedHistory. Lineage chunks (events produced by upstream apps) are
// forwarded verbatim - they were already signed by their producer.
//
// The current-app chunk is identified by chunk.AppId == ownAppID. Each
// chunk owns its own rawEvents bytes (already populated by
// AssembleProtoPropagatedHistory). Signing is a fresh single-batch
// signature over those bytes; receivers verify the chain self-consistently
// and the leaf SPIFFE identity against chunk.appId and the receiver's
// expected namespace.
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

		rawEvents := chunk.GetRawEvents()
		if len(rawEvents) == 0 {
			return fmt.Errorf("own-app propagated chunk has empty rawEvents (app %q)", chunk.GetAppId())
		}

		// Single-batch fresh signature. StartEventIndex is 0 (chunk-
		// local indexing) and there is no previous signature. Bind
		// the chunk's instance and workflow name into the signature so
		// a chunk lifted from this instance can't be relabeled and
		// replayed under a different instance.
		res, err := historysigning.Sign(s.Signer, historysigning.SignOptions{
			RawEvents:            rawEvents,
			StartEventIndex:      0,
			PreviousSignatureRaw: nil,
			ExistingCerts:        nil,
			PropagationContext: &historysigning.PropagationContext{
				InstanceID:   chunk.GetInstanceId(),
				WorkflowName: chunk.GetWorkflowName(),
			},
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
