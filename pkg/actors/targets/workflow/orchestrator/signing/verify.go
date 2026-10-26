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
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/historysigning"
)

// ErrUnknownTaskScheduledID marks a completion whose task scheduled id is
// absent from signed history or records a different invocation (ids restart
// after ContinueAsNew). Not proof of tampering: ContinueAsNew resets history
// and a store fault can roll back a save after dispatch. Callers should drop
// the event like the unsigned path does, not tombstone.
var ErrUnknownTaskScheduledID = errors.New("completion does not correspond to a task scheduled in signed history")

// VerifyInboxAttestation validates an attestation on an inbound completion
// event, absorbs the signer certificate into the state's ext-sigcert table for
// later reference, and strips the companion signerCertificate field so the
// stored form of the event carries only the attestation. Returns a non-nil
// error on verification failure - the caller is expected to tombstone the
// workflow. No-op when Signer is nil.
func (s *Signing) VerifyInboxAttestation(ctx context.Context, state *wfenginestate.State, e *backend.HistoryEvent) error {
	if s.Signer == nil {
		return nil
	}

	// Cert validity is checked against wallclock at ingestion. The event's own
	// timestamp is set by the sender and is not yet covered by this workflow's
	// HistorySignature at ingestion time, so it cannot be trusted here. Once the
	// event is absorbed into signed history, downstream re-verifications use the
	// event timestamp.
	now := time.Now()

	switch body := e.GetEventType().(type) {
	case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
		c := body.ChildWorkflowInstanceCompleted
		if err := runVerify(ctx, diag.AttestationKindChild, c.GetTaskScheduledId(), "child completion", c.GetAttestation() != nil, func() error {
			return s.verifyChild(ctx, verifyChildOptions{
				state:          state,
				taskID:         c.GetTaskScheduledId(),
				att:            c.GetAttestation(),
				certDER:        c.GetSignerCertificate(),
				eventTS:        now,
				output:         c.GetResult(),
				expectedStatus: protos.TerminalStatus_TERMINAL_STATUS_COMPLETED,
			})
		}); err != nil {
			return err
		}
		c.SignerCertificate = nil

	case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
		f := body.ChildWorkflowInstanceFailed
		if err := runVerify(ctx, diag.AttestationKindChild, f.GetTaskScheduledId(), "child failure", f.GetAttestation() != nil, func() error {
			return s.verifyChild(ctx, verifyChildOptions{
				state:          state,
				taskID:         f.GetTaskScheduledId(),
				att:            f.GetAttestation(),
				certDER:        f.GetSignerCertificate(),
				eventTS:        now,
				failure:        f.GetFailureDetails(),
				expectedStatus: protos.TerminalStatus_TERMINAL_STATUS_FAILED,
			})
		}); err != nil {
			return err
		}
		f.SignerCertificate = nil

	case *protos.HistoryEvent_TaskCompleted:
		c := body.TaskCompleted
		if err := runVerify(ctx, diag.AttestationKindActivity, c.GetTaskScheduledId(), "activity completion", c.GetAttestation() != nil, func() error {
			return s.verifyActivity(ctx, verifyActivityOptions{
				state:          state,
				taskID:         c.GetTaskScheduledId(),
				att:            c.GetAttestation(),
				certDER:        c.GetSignerCertificate(),
				eventTS:        now,
				output:         c.GetResult(),
				expectedStatus: protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_COMPLETED,
			})
		}); err != nil {
			return err
		}
		c.SignerCertificate = nil

	case *protos.HistoryEvent_TaskFailed:
		f := body.TaskFailed
		if err := runVerify(ctx, diag.AttestationKindActivity, f.GetTaskScheduledId(), "activity failure", f.GetAttestation() != nil, func() error {
			return s.verifyActivity(ctx, verifyActivityOptions{
				state:          state,
				taskID:         f.GetTaskScheduledId(),
				att:            f.GetAttestation(),
				certDER:        f.GetSignerCertificate(),
				eventTS:        now,
				failure:        f.GetFailureDetails(),
				expectedStatus: protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_FAILED,
			})
		}); err != nil {
			return err
		}
		f.SignerCertificate = nil
	}

	return nil
}

// runVerify wraps the per-event verification boilerplate: reject with a
// recorded metric when the attestation is missing, otherwise time the
// verify call and record the result.
func runVerify(ctx context.Context, kind string, taskID int32, eventDesc string, hasAttestation bool, verify func() error) error {
	if !hasAttestation {
		diag.DefaultWorkflowMonitoring.AttestationVerified(ctx, kind, diag.AttestationResultReject, 0)
		return fmt.Errorf("%s missing required attestation for task %d", eventDesc, taskID)
	}
	start := time.Now()
	err := verify()
	result := diag.AttestationResultOK
	if err != nil {
		result = diag.AttestationResultReject
	}
	diag.DefaultWorkflowMonitoring.AttestationVerified(ctx, kind, result, float64(time.Since(start).Milliseconds()))
	return err
}

type verifyChildOptions struct {
	state          *wfenginestate.State
	taskID         int32
	att            *backend.ChildCompletionAttestation
	certDER        []byte
	eventTS        time.Time
	output         *wrapperspb.StringValue
	failure        *protos.TaskFailureDetails
	expectedStatus protos.TerminalStatus
}

func (s *Signing) verifyChild(ctx context.Context, opts verifyChildOptions) error {
	created := opts.state.FindHistoryEventByID(opts.taskID).GetChildWorkflowInstanceCreated()
	if created == nil {
		return fmt.Errorf("child completion: %w (task %d)", ErrUnknownTaskScheduledID, opts.taskID)
	}

	certDigest := historysigning.CertDigest(opts.certDER)
	chainOfTrustVerifiedExternally := s.certChainTrustVerified(certDigest, opts.eventTS)
	if chainOfTrustVerifiedExternally {
		diag.DefaultWorkflowMonitoring.AttestationCertCacheLookup(ctx, diag.CertCacheHit)
	} else {
		diag.DefaultWorkflowMonitoring.AttestationCertCacheLookup(ctx, diag.CertCacheMiss)
	}

	payload, err := historysigning.VerifyChildAttestation(historysigning.VerifyChildOptions{
		Attestation:                    opts.att,
		SignerCertDER:                  opts.certDER,
		EventTimestamp:                 opts.eventTS,
		ExpectedParentInstanceId:       s.ActorID,
		ExpectedParentTaskScheduledId:  opts.taskID,
		ClaimedInput:                   created.GetInput(),
		ClaimedOutput:                  opts.output,
		ClaimedFailure:                 opts.failure,
		Signer:                         s.Signer,
		ChainOfTrustVerifiedExternally: chainOfTrustVerifiedExternally,
	})
	if err != nil {
		if s.isStaleChildCompletion(opts, created, chainOfTrustVerifiedExternally) {
			return fmt.Errorf("child completion for task %d reports a superseded invocation of a reused scheduled id: %w", opts.taskID, ErrUnknownTaskScheduledID)
		}
		return fmt.Errorf("child attestation verification failed for task %d: %w", opts.taskID, err)
	}
	if payload.GetTerminalStatus() != opts.expectedStatus {
		return fmt.Errorf("child attestation terminalStatus %v does not match enclosing event (%v)",
			payload.GetTerminalStatus(), opts.expectedStatus)
	}

	if !chainOfTrustVerifiedExternally {
		s.cacheCertChainTrust(certDigest, opts.certDER)
	}

	if _, err := opts.state.AddExternalCert(payload.GetSignerCertDigest(), opts.certDER); err != nil {
		return fmt.Errorf("child attestation: failed to absorb signer cert for task %d: %w", opts.taskID, err)
	}
	return nil
}

type verifyActivityOptions struct {
	state          *wfenginestate.State
	taskID         int32
	att            *backend.ActivityCompletionAttestation
	certDER        []byte
	eventTS        time.Time
	output         *wrapperspb.StringValue
	failure        *protos.TaskFailureDetails
	expectedStatus protos.ActivityTerminalStatus
}

func (s *Signing) verifyActivity(ctx context.Context, opts verifyActivityOptions) error {
	scheduled := opts.state.FindHistoryEventByID(opts.taskID).GetTaskScheduled()
	if scheduled == nil {
		return fmt.Errorf("activity completion: %w (task %d)", ErrUnknownTaskScheduledID, opts.taskID)
	}

	certDigest := historysigning.CertDigest(opts.certDER)
	chainOfTrustVerifiedExternally := s.certChainTrustVerified(certDigest, opts.eventTS)
	if chainOfTrustVerifiedExternally {
		diag.DefaultWorkflowMonitoring.AttestationCertCacheLookup(ctx, diag.CertCacheHit)
	} else {
		diag.DefaultWorkflowMonitoring.AttestationCertCacheLookup(ctx, diag.CertCacheMiss)
	}

	payload, err := historysigning.VerifyActivityAttestation(historysigning.VerifyActivityOptions{
		Attestation:                    opts.att,
		SignerCertDER:                  opts.certDER,
		EventTimestamp:                 opts.eventTS,
		ExpectedParentInstanceId:       s.ActorID,
		ExpectedParentTaskScheduledId:  opts.taskID,
		ExpectedActivityName:           scheduled.GetName(),
		ClaimedInput:                   scheduled.GetInput(),
		ClaimedOutput:                  opts.output,
		ClaimedFailure:                 opts.failure,
		Signer:                         s.Signer,
		ChainOfTrustVerifiedExternally: chainOfTrustVerifiedExternally,
	})
	if err != nil {
		if s.isStaleActivityCompletion(opts, scheduled, chainOfTrustVerifiedExternally) {
			return fmt.Errorf("activity completion for task %d reports a superseded invocation of a reused scheduled id: %w", opts.taskID, ErrUnknownTaskScheduledID)
		}
		return fmt.Errorf("activity attestation verification failed for task %d: %w", opts.taskID, err)
	}
	if payload.GetTerminalStatus() != opts.expectedStatus {
		return fmt.Errorf("activity attestation terminalStatus %v does not match enclosing event (%v)",
			payload.GetTerminalStatus(), opts.expectedStatus)
	}

	if !chainOfTrustVerifiedExternally {
		s.cacheCertChainTrust(certDigest, opts.certDER)
	}

	if _, err := opts.state.AddExternalCert(payload.GetSignerCertDigest(), opts.certDER); err != nil {
		return fmt.Errorf("activity attestation: failed to absorb signer cert for task %d: %w", opts.taskID, err)
	}
	return nil
}

// isStaleChildCompletion reports whether a child completion that failed full
// verification is a straggler for a superseded invocation of a reused task id
// (ids restart after ContinueAsNew) rather than tampering. The wire carries
// no child instance id, so the check is by error class: the attestation must
// be genuinely signed, trusted, and bound to this parent, task id, and
// status, with only the io digest disagreeing with the recorded invocation.
// Bad signatures, untrusted certs, and wrong bindings still tombstone.
func (s *Signing) isStaleChildCompletion(opts verifyChildOptions, created *protos.ChildWorkflowInstanceCreatedEvent, chainVerified bool) bool {
	if opts.att == nil || s.Signer == nil {
		return false
	}

	var payload protos.ChildCompletionAttestationPayload
	if proto.Unmarshal(opts.att.GetPayload(), &payload) != nil {
		return false
	}
	if payload.GetCanonicalSpecVersion() != historysigning.CanonicalSpecVersion ||
		payload.GetParentInstanceId() != s.ActorID ||
		payload.GetParentTaskScheduledId() != opts.taskID ||
		payload.GetTerminalStatus() != opts.expectedStatus {
		return false
	}

	if !bytes.Equal(historysigning.CertDigest(opts.certDER), payload.GetSignerCertDigest()) {
		return false
	}
	if s.Signer.VerifySignature(historysigning.PayloadSignatureInput(opts.att.GetPayload()), opts.att.GetSignature(), opts.certDER) != nil {
		return false
	}
	if !chainVerified && s.Signer.VerifyCertChainOfTrust(opts.certDER, opts.eventTS) != nil {
		return false
	}

	out, ok := canonicalOutput(opts.expectedStatus == protos.TerminalStatus_TERMINAL_STATUS_COMPLETED, opts.output, opts.failure)
	if !ok {
		return false
	}
	return !bytes.Equal(historysigning.IODigest(historysigning.CanonicalInput(created.GetInput()), out), payload.GetIoDigest())
}

// isStaleActivityCompletion is the activity counterpart of
// isStaleChildCompletion: a genuinely signed attestation bound to this parent
// instance, task id, and terminal status whose activity name or io digest
// does not match the invocation currently recorded at that task id.
func (s *Signing) isStaleActivityCompletion(opts verifyActivityOptions, scheduled *protos.TaskScheduledEvent, chainVerified bool) bool {
	if opts.att == nil || s.Signer == nil {
		return false
	}

	var payload protos.ActivityCompletionAttestationPayload
	if proto.Unmarshal(opts.att.GetPayload(), &payload) != nil {
		return false
	}
	if payload.GetCanonicalSpecVersion() != historysigning.CanonicalSpecVersion ||
		payload.GetParentInstanceId() != s.ActorID ||
		payload.GetParentTaskScheduledId() != opts.taskID ||
		payload.GetTerminalStatus() != opts.expectedStatus {
		return false
	}

	if !bytes.Equal(historysigning.CertDigest(opts.certDER), payload.GetSignerCertDigest()) {
		return false
	}
	if s.Signer.VerifySignature(historysigning.PayloadSignatureInput(opts.att.GetPayload()), opts.att.GetSignature(), opts.certDER) != nil {
		return false
	}
	if !chainVerified && s.Signer.VerifyCertChainOfTrust(opts.certDER, opts.eventTS) != nil {
		return false
	}

	if payload.GetActivityName() != scheduled.GetName() {
		return true
	}
	var completed bool
	switch opts.expectedStatus {
	case protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_COMPLETED:
		completed = true
	case protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_FAILED:
	default:
		return false
	}
	out, ok := canonicalOutput(completed, opts.output, opts.failure)
	if !ok {
		return false
	}
	return !bytes.Equal(historysigning.IODigest(historysigning.CanonicalInput(scheduled.GetInput()), out), payload.GetIoDigest())
}

// canonicalOutput mirrors the canonical output selection inside
// historysigning's verify helpers for the given terminal outcome.
func canonicalOutput(completed bool, output *wrapperspb.StringValue, failure *protos.TaskFailureDetails) ([]byte, bool) {
	if completed {
		return historysigning.CanonicalSuccessOutput(output), true
	}
	out, err := historysigning.CanonicalFailureOutput(failure)
	return out, err == nil
}
