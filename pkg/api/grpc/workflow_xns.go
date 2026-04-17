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

package grpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	anypb "google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// CallWorkflowCrossNamespace is the target-side handler for cross-namespace
// workflow/activity dispatch. It performs the following steps:
//
//  1. Extracts the caller's (namespace, appID) from the SPIFFE ID on the
//     mTLS connection. This is the security boundary; the Source fields in
//     the request are informational only.
//  2. Evaluates the local WorkflowAccessPolicy against the parsed actor
//     type + method + payload using the same ns-aware evaluator as the
//     remote-CallActor path. If denied, returns PermissionDenied.
//  3. Creates a local reminder whose name is the idempotency key. Duplicate
//     creates are a no-op, so caller retries land on the same reminder and
//     the work executes exactly once.
//  4. Returns ACK. Durability is handed off the moment the reminder is
//     persisted.
func (a *api) CallWorkflowCrossNamespace(ctx context.Context, req *internalv1pb.CrossNSDispatchRequest) (*internalv1pb.CrossNSAck, error) {
	callerAppID, callerNamespace, err := a.extractCallerIdentity(ctx)
	if err != nil {
		return nil, err
	}

	policies := a.workflowAccessPolicies.Load()
	// When policies are nil on the target, deny by default — cross-namespace
	// calls require an explicit ingress rule. This is stricter than the
	// same-namespace default (which allows nil policies through) because
	// cross-namespace is a security boundary that should not be implicitly
	// open.
	if policies == nil {
		a.logger.Warnf("Cross-namespace workflow call from '%s/%s' denied: no policy configured on target", callerNamespace, callerAppID)
		return nil, status.Error(codes.PermissionDenied, workflowACLDeniedMsg)
	}

	result, err := workflowacl.EnforceRequest(
		policies, callerNamespace, callerAppID,
		req.GetActorType(),
		req.GetMethod(),
		req.GetPayload(),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "workflow access policy: %v", err)
	}
	if result == nil || !result.Allowed {
		opType := ""
		opName := ""
		if result != nil {
			opType = string(result.OpType)
			opName = result.Operation
		}
		a.logger.Warnf("Cross-namespace workflow call from '%s/%s' denied for %s operation '%s'", callerNamespace, callerAppID, opType, opName)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, opType, opName)
		return nil, status.Error(codes.PermissionDenied, workflowACLDeniedMsg)
	}
	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, string(result.OpType), result.Operation)

	reminders, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to access reminders: %v", err)
	}

	// The reminder payload wraps the incoming CrossNSDispatchRequest so the
	// firing handler (handleXNSExecReminder on the target orchestrator/activity
	// actor) has the actor_type/actor_id/method/payload tuple needed to
	// perform the local invocation.
	data, perr := anypb.New(req)
	if perr != nil {
		return nil, status.Errorf(codes.Internal, "failed to encode reminder payload: %v", perr)
	}

	reminderName := common.ReminderPrefixXNSExec + req.GetIdempotencyKey()
	createErr := reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: req.GetActorType(),
		ActorID:   req.GetActorId(),
		Name:      reminderName,
		Data:      data,
		DueTime:   time.Now().UTC().Format(time.RFC3339),
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second),
					MaxRetries: nil,
				},
			},
		},
	})
	if createErr != nil {
		// An already-exists error is success for idempotency purposes: the
		// work is durable.
		if status.Code(createErr) == codes.AlreadyExists {
			return &internalv1pb.CrossNSAck{}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to schedule cross-namespace exec reminder: %v", createErr)
	}

	return &internalv1pb.CrossNSAck{}, nil
}

// DeliverWorkflowResultCrossNamespace is the parent-side handler for
// cross-namespace completion callbacks.
//
//  1. Feature-gate + SPIFFE identity extraction.
//  2. Policy check: the parent's policy must authorize the responding child
//     app — result delivery is a policy-gated operation.
//  3. Execution-ID check: the result must carry the parent's *current*
//     executionId; a mismatch means the parent was purged and re-created
//     (or never existed), in which case we ACK to stop the source retry
//     loop and drop the event so it cannot corrupt the new run's inbox.
//  4. Create a local idempotent xns-result reminder that carries the event
//     into the parent orchestrator's inbox via the existing activity-result
//     delivery path.
//
// TODO(cross-ns): step 3 (executionId lookup) requires a hook into the
// actor state store to read the parent's current executionId for the given
// instanceId. That lookup is orthogonal to the rest of this handler; it
// is stubbed as a no-op here (always accepts) and will be wired up in a
// follow-up so this change stays focused on the new RPC surface.
func (a *api) DeliverWorkflowResultCrossNamespace(ctx context.Context, req *internalv1pb.CrossNSResultRequest) (*internalv1pb.CrossNSAck, error) {
	callerAppID, callerNamespace, err := a.extractCallerIdentity(ctx)
	if err != nil {
		return nil, err
	}

	policies := a.workflowAccessPolicies.Load()
	if policies == nil {
		a.logger.Warnf("Cross-namespace workflow result from '%s/%s' denied: no policy configured on parent", callerNamespace, callerAppID)
		return nil, status.Error(codes.PermissionDenied, workflowACLDeniedMsg)
	}
	if !policies.IsCallerKnown(callerNamespace, callerAppID) {
		a.logger.Warnf("Cross-namespace workflow result from '%s/%s' denied: caller not authorized", callerNamespace, callerAppID)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, "xns-result", "deliver")
		return nil, status.Error(codes.PermissionDenied, workflowACLDeniedMsg)
	}
	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, "xns-result", "deliver")

	// TODO(cross-ns): validate req.ParentExecutionId against the parent
	// orchestrator's currently-stored executionId. For now accept and let
	// the reminder deliver; follow-up ticket to add executionId isolation.

	reminders, err := a.ActorReminders(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to access reminders: %v", err)
	}

	// Wrap the full CrossNSResultRequest — the parent-side reminder handler
	// reads req.ParentExecutionId from here and compares it to the parent
	// orchestrator's currently-loaded executionId before delivery, so
	// results destined for a purged-and-re-created parent drop cleanly
	// rather than corrupting the new run's inbox.
	data, perr := anypb.New(req)
	if perr != nil {
		return nil, status.Errorf(codes.Internal, "failed to encode reminder payload: %v", perr)
	}

	// Reminder lives on the parent orchestrator actor — the appID for the
	// actor type is the sidecar's own app. The idempotency key becomes the
	// reminder-name suffix so caller retries collapse to a single delivery.
	actorType := "dapr.internal." + a.Namespace() + "." + a.AppID() + ".workflow"
	reminderName := common.ReminderPrefixXNSResultIn + req.GetIdempotencyKey()
	createErr := reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: actorType,
		ActorID:   req.GetParentInstanceId(),
		Name:      reminderName,
		Data:      data,
		DueTime:   time.Now().UTC().Format(time.RFC3339),
		FailurePolicy: &commonv1pb.JobFailurePolicy{
			Policy: &commonv1pb.JobFailurePolicy_Constant{
				Constant: &commonv1pb.JobFailurePolicyConstant{
					Interval:   durationpb.New(time.Second),
					MaxRetries: nil,
				},
			},
		},
	})
	if createErr != nil {
		if status.Code(createErr) == codes.AlreadyExists {
			return &internalv1pb.CrossNSAck{}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to schedule cross-namespace result reminder: %v", createErr)
	}

	return &internalv1pb.CrossNSAck{}, nil
}
