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

	"google.golang.org/protobuf/types/known/wrapperspb"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/historysigning"
)

// ChildAttestationParams are the parent-side inputs needed to build a
// ChildCompletionAttestation. Resolved by the orchestrator from its
// ExecutionStartedEvent before calling AttachChildCompletionAttestation.
type ChildAttestationParams struct {
	ParentInstanceID      string
	ParentTaskScheduledID int32
	Input                 *wrapperspb.StringValue
}

// AttachChildCompletionAttestation builds a ChildCompletionAttestation
// signed by this workflow's identity and attaches it (plus the signer's
// certificate chain as a companion) to the given outbound history event.
// The event must be either a ChildWorkflowInstanceCompletedEvent or a
// ChildWorkflowInstanceFailedEvent - other event types are ignored.
// No-op if Signer is nil.
func (s *Signing) AttachChildCompletionAttestation(ctx context.Context, evt *backend.HistoryEvent, params ChildAttestationParams) error {
	if s.Signer == nil {
		return nil
	}

	in := historysigning.ChildAttestationInput{
		ParentInstanceId:      params.ParentInstanceID,
		ParentTaskScheduledId: params.ParentTaskScheduledID,
		Input:                 params.Input,
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

	att, certChainDER, err := historysigning.BuildChildAttestation(s.Signer, in)
	if err != nil {
		diag.DefaultWorkflowMonitoring.AttestationGenerated(ctx, diag.AttestationKindChild, diag.StatusFailed)
		return fmt.Errorf("failed to build child attestation: %w", err)
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
