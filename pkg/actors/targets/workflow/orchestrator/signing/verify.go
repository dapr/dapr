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
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/historysigning"
)

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
		if c.GetAttestation() == nil {
			diag.DefaultWorkflowMonitoring.AttestationVerified(ctx, diag.AttestationKindChild, diag.AttestationResultReject, 0)
			return fmt.Errorf("child completion missing required attestation for task %d", c.GetTaskScheduledId())
		}
		start := time.Now()
		err := s.verifyChild(ctx, verifyChildOptions{
			state:          state,
			taskID:         c.GetTaskScheduledId(),
			att:            c.GetAttestation(),
			certDER:        c.GetSignerCertificate(),
			eventTS:        now,
			output:         c.GetResult(),
			expectedStatus: protos.TerminalStatus_TERMINAL_STATUS_COMPLETED,
		})
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
		err := s.verifyChild(ctx, verifyChildOptions{
			state:          state,
			taskID:         f.GetTaskScheduledId(),
			att:            f.GetAttestation(),
			certDER:        f.GetSignerCertificate(),
			eventTS:        now,
			failure:        f.GetFailureDetails(),
			expectedStatus: protos.TerminalStatus_TERMINAL_STATUS_FAILED,
		})
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
		err := s.verifyActivity(ctx, verifyActivityOptions{
			state:          state,
			taskID:         c.GetTaskScheduledId(),
			att:            c.GetAttestation(),
			certDER:        c.GetSignerCertificate(),
			eventTS:        now,
			output:         c.GetResult(),
			expectedStatus: protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_COMPLETED,
		})
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
		err := s.verifyActivity(ctx, verifyActivityOptions{
			state:          state,
			taskID:         f.GetTaskScheduledId(),
			att:            f.GetAttestation(),
			certDER:        f.GetSignerCertificate(),
			eventTS:        now,
			failure:        f.GetFailureDetails(),
			expectedStatus: protos.ActivityTerminalStatus_ACTIVITY_TERMINAL_STATUS_FAILED,
		})
		recordAttestationVerify(ctx, diag.AttestationKindActivity, err, start)
		if err != nil {
			return err
		}
		f.SignerCertificate = nil
	}

	return nil
}

func recordAttestationVerify(ctx context.Context, kind string, verifyErr error, start time.Time) {
	result := diag.AttestationResultOK
	if verifyErr != nil {
		result = diag.AttestationResultReject
	}
	diag.DefaultWorkflowMonitoring.AttestationVerified(ctx, kind, result, float64(time.Since(start).Milliseconds()))
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
		return fmt.Errorf("child completion references unknown task scheduled id %d", opts.taskID)
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
		return fmt.Errorf("activity completion references unknown task scheduled id %d", opts.taskID)
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
