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
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/historysigning"
)

// certValidityWindow caches the leaf cert's validity window after a
// successful chain-of-trust verification. Subsequent attestations
// referencing the same cert digest can skip chain-of-trust as long as the
// event timestamp falls inside this window.
type certValidityWindow struct {
	notBefore time.Time
	notAfter  time.Time
}

// certChainTrustVerified returns true if this orchestrator has already
// chain-verified the cert identified by digest at some time, and the
// supplied eventTS falls within the cached leaf validity window. Caller
// must pass ChainOfTrustVerifiedExternally=true on a true return.
func (o *orchestrator) certChainTrustVerified(digest []byte, eventTS time.Time) bool {
	v, ok := o.certVerifyCache.Load(string(digest))
	if !ok {
		return false
	}
	w := v.(certValidityWindow)
	return !eventTS.Before(w.notBefore) && !eventTS.After(w.notAfter)
}

// cacheCertChainTrust records that the cert identified by digest has been
// chain-verified; subsequent lookups within the leaf validity window can
// skip chain-of-trust. chainDER is the DER-concatenated certificate chain
// (leaf first, intermediates concatenated), matching the format consumed
// by parseLeafCertFromChainDER.
func (o *orchestrator) cacheCertChainTrust(digest []byte, chainDER []byte) {
	leaf, err := parseLeafCertFromChainDER(chainDER)
	if err != nil {
		// Parsing failure here is benign — we just don't cache. The
		// next attestation using the same cert will pay full chain-of-
		// trust verification again.
		return
	}
	o.certVerifyCache.Store(string(digest), certValidityWindow{
		notBefore: leaf.NotBefore,
		notAfter:  leaf.NotAfter,
	})
}

// parseLeafCertFromChainDER parses only the leaf (first) cert from a
// DER-concatenated chain, matching the format used by
// SigningCertificate.certificate and the attestation companion. Each cert
// is a self-delimiting ASN.1 SEQUENCE, so we read only the first.
func parseLeafCertFromChainDER(chainDER []byte) (*x509.Certificate, error) {
	if len(chainDER) == 0 {
		return nil, errors.New("certificate chain is empty")
	}
	var raw asn1.RawValue
	if _, err := asn1.Unmarshal(chainDER, &raw); err != nil {
		return nil, fmt.Errorf("failed to read leaf certificate ASN.1: %w", err)
	}
	return x509.ParseCertificate(raw.FullBytes)
}

// signNewEvents deterministically marshals the newly added history events,
// creates a HistorySignature covering them, and appends it (along with any
// new signing certificate) to the state.
// The marshaled bytes are stored on the state so that GetSaveRequest can
// persist the exact bytes that were signed.
// If signer is nil (mTLS disabled or feature flag off), this is a no-op.
func (o *orchestrator) signNewEvents(state *wfenginestate.State) error {
	if o.signer == nil {
		return nil
	}

	newEventCount := state.HistoryAddedCount()
	if newEventCount == 0 {
		return nil
	}

	// Defensive: the added count cannot exceed the history length. Without
	// this guard, the int subtraction below would underflow and the uint64
	// cast would produce a massive startIndex, panicking in the marshal loop
	// with a misleading out-of-bounds error instead of a clear message here.
	if newEventCount > len(state.History) {
		return fmt.Errorf("signNewEvents called with newEventCount=%d but history has only %d events",
			newEventCount, len(state.History))
	}

	//nolint:gosec
	startIndex := uint64(len(state.History) - newEventCount)

	// Defensive: when there is no previous signature, this must be the very
	// first signature for the workflow, which requires the batch to cover the
	// entire history. Otherwise we'd produce a chain-less signature over a
	// suffix of the history which the verifier would reject, but the defect
	// would only surface on reload. Fail fast instead.
	if len(state.RawSignatures) == 0 && startIndex != 0 {
		return fmt.Errorf("cannot sign partial history (%d events, starting at index %d) without a previous signature",
			newEventCount, startIndex)
	}

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

// attachChildCompletionAttestation builds a ChildCompletionAttestation
// signed by this workflow's identity and attaches it (plus the signer's
// certificate chain as a companion) to the given outbound history event.
// The event must be either a ChildWorkflowInstanceCompletedEvent or a
// ChildWorkflowInstanceFailedEvent — other event types are ignored.
// No-op if signing is disabled.
//
// Called by the orchestrator run loop just before outbound
// ChildWorkflowInstance{Completed,Failed} messages are dispatched to the
// parent. The attestation commits to the child's input/output pair,
// terminal status, and the parent's (instance ID, taskScheduledId) tuple,
// so the receiving parent can cryptographically verify the child
// executed this specific invocation.
func (o *orchestrator) attachChildCompletionAttestation(ctx context.Context, state *wfenginestate.State, evt *backend.HistoryEvent) error {
	if o.signer == nil {
		return nil
	}

	started := o.getExecutionStartedEvent(state)
	parent := started.GetParentInstance()
	if parent == nil || parent.GetWorkflowInstance() == nil {
		return fmt.Errorf("workflow actor '%s': cannot build child attestation without parent instance info", o.actorID)
	}

	in := historysigning.ChildAttestationInput{
		ParentInstanceId:      parent.GetWorkflowInstance().GetInstanceId(),
		ParentTaskScheduledId: parent.GetTaskScheduledId(),
		Input:                 started.GetInput(),
	}

	switch body := evt.GetEventType().(type) {
	case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
		in.Output = body.ChildWorkflowInstanceCompleted.GetResult()
		in.TerminalStatus = protos.TerminalStatus_TERMINAL_STATUS_COMPLETED
	case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
		in.FailureDetails = body.ChildWorkflowInstanceFailed.GetFailureDetails()
		in.TerminalStatus = protos.TerminalStatus_TERMINAL_STATUS_FAILED
	default:
		return nil
	}

	att, certChainDER, err := historysigning.BuildChildAttestation(o.signer, in)
	if err != nil {
		diag.DefaultWorkflowMonitoring.AttestationGenerated(ctx, diag.AttestationKindChild, diag.StatusFailed)
		return fmt.Errorf("workflow actor '%s': failed to build child attestation: %w", o.actorID, err)
	}

	switch body := evt.GetEventType().(type) {
	case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
		body.ChildWorkflowInstanceCompleted.Attestation = att
		body.ChildWorkflowInstanceCompleted.SignerCertificate = certChainDER
	case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
		body.ChildWorkflowInstanceFailed.Attestation = att
		body.ChildWorkflowInstanceFailed.SignerCertificate = certChainDER
	}

	diag.DefaultWorkflowMonitoring.AttestationGenerated(ctx, diag.AttestationKindChild, diag.StatusSuccess)
	return nil
}

// verifyInboxAttestation validates an attestation on an inbound completion
// event, absorbs the signer certificate into the state's ext-sigcert table
// for later reference, and strips the companion signerCertificate field so
// the stored form of the event carries only the attestation (the cert
// lives once in ext-sigcert-NNNNNN, referenced by the attestation's
// signerCertDigest). No-op when signing is disabled.
//
// Reject conditions (all return an error and caller drops the event):
//   - attestation is absent on a terminal event (signing enabled implies
//     the child/activity must attest)
//   - companion cert digest does not match committed digest
//   - cert chain does not chain to a Sentry trust anchor
//   - signature does not verify over sha256(payload)
//   - payload's parent binding does not match this workflow's instance ID
//     or the referenced task scheduled ID does not exist in signed history
//   - payload's ioDigest does not match canonical(claimed input, claimed
//     output) where input comes from the parent's signed
//     TaskScheduledEvent / ChildWorkflowInstanceCreatedEvent
//   - payload's terminalStatus does not match the enclosing event type
func (o *orchestrator) verifyInboxAttestation(ctx context.Context, state *wfenginestate.State, e *backend.HistoryEvent) error {
	if o.signer == nil {
		return nil
	}

	// Cert validity is checked against wallclock at ingestion — the event's
	// own timestamp is set by the (potentially compromised) sender and is
	// not yet covered by this workflow's HistorySignature at ingestion
	// time, so it cannot be trusted here. Once the event is absorbed into
	// A's signed history, downstream re-verifications use the event
	// timestamp.
	now := time.Now()

	switch body := e.GetEventType().(type) {
	case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
		c := body.ChildWorkflowInstanceCompleted
		if c.GetAttestation() == nil {
			diag.DefaultWorkflowMonitoring.AttestationVerified(ctx, diag.AttestationKindChild, diag.AttestationResultReject, 0)
			return fmt.Errorf("child completion missing required attestation for task %d", c.GetTaskScheduledId())
		}
		start := time.Now()
		err := o.verifyChildInboxAttestation(ctx, state, c.GetTaskScheduledId(), c.GetAttestation(), c.GetSignerCertificate(), now, c.GetResult(), nil, protos.TerminalStatus_TERMINAL_STATUS_COMPLETED)
		recordAttestationVerify(ctx, diag.AttestationKindChild, err, start)
		if err != nil {
			return err
		}
		c.SignerCertificate = nil

	case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
		f := body.ChildWorkflowInstanceFailed
		if f.GetAttestation() == nil {
			diag.DefaultWorkflowMonitoring.AttestationVerified(ctx, diag.AttestationKindChild, diag.AttestationResultReject, 0)
			return fmt.Errorf("child failure missing required attestation for task %d", f.GetTaskScheduledId())
		}
		start := time.Now()
		err := o.verifyChildInboxAttestation(ctx, state, f.GetTaskScheduledId(), f.GetAttestation(), f.GetSignerCertificate(), now, nil, f.GetFailureDetails(), protos.TerminalStatus_TERMINAL_STATUS_FAILED)
		recordAttestationVerify(ctx, diag.AttestationKindChild, err, start)
		if err != nil {
			return err
		}
		f.SignerCertificate = nil

	case *protos.HistoryEvent_TaskCompleted:
		c := body.TaskCompleted
		if c.GetAttestation() == nil {
			diag.DefaultWorkflowMonitoring.AttestationVerified(ctx, diag.AttestationKindActivity, diag.AttestationResultReject, 0)
			return fmt.Errorf("activity completion missing required attestation for task %d", c.GetTaskScheduledId())
		}
		start := time.Now()
		err := o.verifyActivityInboxAttestation(ctx, state, c.GetTaskScheduledId(), c.GetAttestation(), c.GetSignerCertificate(), now, c.GetResult(), nil, protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_COMPLETED)
		recordAttestationVerify(ctx, diag.AttestationKindActivity, err, start)
		if err != nil {
			return err
		}
		c.SignerCertificate = nil

	case *protos.HistoryEvent_TaskFailed:
		f := body.TaskFailed
		if f.GetAttestation() == nil {
			diag.DefaultWorkflowMonitoring.AttestationVerified(ctx, diag.AttestationKindActivity, diag.AttestationResultReject, 0)
			return fmt.Errorf("activity failure missing required attestation for task %d", f.GetTaskScheduledId())
		}
		start := time.Now()
		err := o.verifyActivityInboxAttestation(ctx, state, f.GetTaskScheduledId(), f.GetAttestation(), f.GetSignerCertificate(), now, nil, f.GetFailureDetails(), protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_FAILED)
		recordAttestationVerify(ctx, diag.AttestationKindActivity, err, start)
		if err != nil {
			return err
		}
		f.SignerCertificate = nil
	}

	return nil
}

// recordAttestationVerify records a single attestation verification into
// the workflow monitoring metrics, tagging by kind and ok/reject result.
func recordAttestationVerify(ctx context.Context, kind string, verifyErr error, start time.Time) {
	result := diag.AttestationResultOK
	if verifyErr != nil {
		result = diag.AttestationResultReject
	}
	diag.DefaultWorkflowMonitoring.AttestationVerified(ctx, kind, result, float64(time.Since(start).Milliseconds()))
}

func (o *orchestrator) verifyChildInboxAttestation(
	ctx context.Context,
	state *wfenginestate.State,
	taskID int32,
	att *backend.ChildCompletionAttestation,
	certDER []byte,
	eventTS time.Time,
	output *wrapperspb.StringValue,
	failure *protos.TaskFailureDetails,
	expectedStatus protos.TerminalStatus,
) error {
	created := findChildWorkflowCreatedEvent(state, taskID)
	if created == nil {
		return fmt.Errorf("child completion references unknown task scheduled id %d", taskID)
	}

	certDigest := historysigning.CertDigest(certDER)
	skipChain := o.certChainTrustVerified(certDigest, eventTS)
	if skipChain {
		diag.DefaultWorkflowMonitoring.AttestationCertCacheLookup(ctx, diag.CertCacheHit)
	} else {
		diag.DefaultWorkflowMonitoring.AttestationCertCacheLookup(ctx, diag.CertCacheMiss)
	}

	payload, err := historysigning.VerifyChildAttestation(historysigning.VerifyChildOptions{
		Attestation:                    att,
		SignerCertDER:                  certDER,
		EventTimestamp:                 eventTS,
		ExpectedParentInstanceId:       o.actorID,
		ExpectedParentTaskScheduledId:  taskID,
		ClaimedInput:                   created.GetInput(),
		ClaimedOutput:                  output,
		ClaimedFailure:                 failure,
		Signer:                         o.signer,
		ChainOfTrustVerifiedExternally: skipChain,
	})
	if err != nil {
		return fmt.Errorf("child attestation verification failed for task %d: %w", taskID, err)
	}
	if payload.GetTerminalStatus() != expectedStatus {
		return fmt.Errorf("child attestation terminalStatus %v does not match enclosing event (%v)",
			payload.GetTerminalStatus(), expectedStatus)
	}

	if !skipChain {
		o.cacheCertChainTrust(certDigest, certDER)
	}

	state.AddExternalCert(payload.GetSignerCertDigest(), certDER)
	return nil
}

func (o *orchestrator) verifyActivityInboxAttestation(
	ctx context.Context,
	state *wfenginestate.State,
	taskID int32,
	att *backend.ActivityCompletionAttestation,
	certDER []byte,
	eventTS time.Time,
	output *wrapperspb.StringValue,
	failure *protos.TaskFailureDetails,
	expectedStatus protos.ActivityTerminalStatus,
) error {
	scheduled := findTaskScheduledEvent(state, taskID)
	if scheduled == nil {
		return fmt.Errorf("activity completion references unknown task scheduled id %d", taskID)
	}

	certDigest := historysigning.CertDigest(certDER)
	skipChain := o.certChainTrustVerified(certDigest, eventTS)
	if skipChain {
		diag.DefaultWorkflowMonitoring.AttestationCertCacheLookup(ctx, diag.CertCacheHit)
	} else {
		diag.DefaultWorkflowMonitoring.AttestationCertCacheLookup(ctx, diag.CertCacheMiss)
	}

	payload, err := historysigning.VerifyActivityAttestation(historysigning.VerifyActivityOptions{
		Attestation:                    att,
		SignerCertDER:                  certDER,
		EventTimestamp:                 eventTS,
		ExpectedParentInstanceId:       o.actorID,
		ExpectedParentTaskScheduledId:  taskID,
		ExpectedActivityName:           scheduled.GetName(),
		ClaimedInput:                   scheduled.GetInput(),
		ClaimedOutput:                  output,
		ClaimedFailure:                 failure,
		Signer:                         o.signer,
		ChainOfTrustVerifiedExternally: skipChain,
	})
	if err != nil {
		return fmt.Errorf("activity attestation verification failed for task %d: %w", taskID, err)
	}
	if payload.GetTerminalStatus() != expectedStatus {
		return fmt.Errorf("activity attestation terminalStatus %v does not match enclosing event (%v)",
			payload.GetTerminalStatus(), expectedStatus)
	}

	if !skipChain {
		o.cacheCertChainTrust(certDigest, certDER)
	}

	state.AddExternalCert(payload.GetSignerCertDigest(), certDER)
	return nil
}

// findTaskScheduledEvent returns the TaskScheduledEvent in state.History
// whose EventId matches taskID, or nil if not found.
func findTaskScheduledEvent(state *wfenginestate.State, taskID int32) *protos.TaskScheduledEvent {
	for _, e := range state.History {
		if e.GetEventId() == taskID {
			return e.GetTaskScheduled()
		}
	}
	return nil
}

// findChildWorkflowCreatedEvent returns the ChildWorkflowInstanceCreatedEvent
// in state.History whose EventId matches taskID, or nil if not found.
func findChildWorkflowCreatedEvent(state *wfenginestate.State, taskID int32) *protos.ChildWorkflowInstanceCreatedEvent {
	for _, e := range state.History {
		if e.GetEventId() == taskID {
			return e.GetChildWorkflowInstanceCreated()
		}
	}
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
