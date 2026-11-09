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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/messages"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// isLocalSyntheticFailure reports whether e is a failure event this app authored
// itself — a WorkflowAccessPolicy denial or an occupied-instance-ID rejection —
// rather than a completion received over the wire. Such events carry no
// attestation by design, so they must not be checked against one. A remote
// completion carries the sender's appID and is unaffected.
func (o *orchestrator) isLocalSyntheticFailure(e *backend.HistoryEvent) bool {
	if e.GetRouter().GetSourceAppID() != o.appID {
		return false
	}
	fd := e.GetChildWorkflowInstanceFailed().GetFailureDetails()
	if fd == nil {
		fd = e.GetTaskFailed().GetFailureDetails()
	}
	et := fd.GetErrorType()
	return et == messages.ErrorTypeAccessPolicyDenied || et == messages.ErrorTypeAlreadyExists
}

// preLoadedMeta lets callers that have already loaded the actor's metadata
// (e.g. handleStream which needs ometa for the response anyway) skip the
// state load inside the access check. Pass nil to load on demand.
func (o *orchestrator) checkAccessPolicy(ctx context.Context, method string, data []byte, parsedAddEvent *backend.HistoryEvent, preLoadedMeta *backend.WorkflowMetadata, md map[string]*internalsv1pb.ListStringValue) error {
	if o.workflowAccessPolicies == nil {
		return nil
	}
	policies := o.workflowAccessPolicies.Load()
	if policies == nil {
		return nil
	}

	callerAppID := workflowacl.CallerAppID(md)
	if policies.SelfCallExempt(o.appID, callerAppID, &o.selfCallerWarned) {
		return nil
	}
	if workflowacl.IsCompletionEvent(parsedAddEvent) {
		return o.checkCompletionSender(ctx, callerAppID, parsedAddEvent)
	}

	operation, err := workflowacl.WorkflowOperationFromMethod(method, parsedAddEvent)
	if err != nil {
		log.Warnf("Workflow actor '%s': workflow access policy denied call '%s': could not derive operation from request: %v", o.actorID, method, err)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(workflowacl.OperationTypeWorkflow), method)
		return status.Errorf(codes.PermissionDenied, "%s: malformed request for method '%s'", workflowacl.DeniedMessageBase, method)
	}
	if operation == "" {
		// Non-subject methods (reminders, internal protocol) are only valid
		// from the local daprd. Cross-app callers cannot invoke them.
		log.Warnf("Workflow actor '%s': workflow access policy denied cross-app call to non-subject method '%s' from app '%s'", o.actorID, method, callerAppID)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(workflowacl.OperationTypeWorkflow), method)
		return status.Errorf(codes.PermissionDenied, "%s: app '%s' cannot invoke method '%s'", workflowacl.DeniedMessageBase, callerAppID, method)
	}

	if callerAppID == "" {
		log.Warnf("Workflow actor '%s': workflow access policy denied call '%s' with missing caller identity", o.actorID, method)
		diag.DefaultMonitoring.WorkflowACLActionDenied("", string(workflowacl.OperationTypeWorkflow), string(operation))
		return status.Errorf(codes.PermissionDenied, "%s: caller identity missing on workflow '%s' operation", workflowacl.DeniedMessageBase, operation)
	}

	name, history, err := o.workflowNameForOperation(ctx, method, data, preLoadedMeta)
	if err != nil {
		log.Errorf("Workflow actor '%s': failed to resolve workflow name for policy check on '%s': %v", o.actorID, method, err)
		return status.Error(codes.Internal, "failed to evaluate workflow access policy")
	}

	allowed, reason := policies.Evaluate(callerAppID, workflowacl.OperationTypeWorkflow, operation, name, history, o.signing.Enabled())
	if !allowed {
		log.Warnf("Workflow actor '%s': workflow access policy denied app '%s' operation '%s' on '%s' (reason=%s)", o.actorID, callerAppID, operation, name, reason)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(workflowacl.OperationTypeWorkflow), string(operation))
		return status.Errorf(codes.PermissionDenied, "%s: app '%s' operation '%s' on workflow '%s' (instance '%s')", workflowacl.DeniedMessageBase, callerAppID, operation, name, o.actorID)
	}

	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, string(workflowacl.OperationTypeWorkflow), string(operation))
	return nil
}

// checkCompletionSender admits an activity result or child workflow outcome
// from another app only when this workflow dispatched that task to the app.
// A completion is the reply to the workflow's own request, so no policy rule
// describes it, and any other app sending one is denied.
func (o *orchestrator) checkCompletionSender(ctx context.Context, callerAppID string, e *backend.HistoryEvent) error {
	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		log.Errorf("Workflow actor '%s': failed to load state for completion sender check: %v", o.actorID, err)
		return status.Error(codes.Internal, "failed to evaluate workflow access policy")
	}
	// No instance: there is nothing a completion could change, and admission
	// reports it gone so the sender stops re-delivering.
	if state == nil {
		return nil
	}

	step, _ := resolves(e)
	dispatch := state.FindHistoryEventByID(step.id)
	if dispatch.GetTaskScheduled() != nil || dispatch.GetChildWorkflowInstanceCreated() != nil {
		target := dispatch.GetRouter().GetTargetAppID()
		if target == "" {
			target = o.appID
		}
		if target == callerAppID {
			return nil
		}
	}

	log.Warnf("Workflow actor '%s': workflow access policy denied %s result for task %d from app '%s', which the task was not dispatched to", o.actorID, step.kind, step.id, callerAppID)
	diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(workflowacl.OperationTypeWorkflow), todo.AddWorkflowEventMethod)
	return status.Errorf(codes.PermissionDenied, "%s: app '%s' was not dispatched task %d of workflow instance '%s'", workflowacl.DeniedMessageBase, callerAppID, step.id, o.actorID)
}

// workflowNameForOperation returns the workflow name for the policy check
// and (when available) the propagated history that should gate `requires`.
// Schedule (CreateWorkflowInstance) carries the name and propagated history
// on the request itself; every other operation resolves the name from the
// target instance's recorded state. An operation on a nonexistent instance
// resolves to an empty name, so only `name: "*"` rules can match it
// (fail-closed by construction). Only schedule carries propagated history;
// for all other operations history is nil and any rule with a `requires`
// block will fail-closed.
func (o *orchestrator) workflowNameForOperation(ctx context.Context, method string, data []byte, preLoadedMeta *backend.WorkflowMetadata) (string, *protos.PropagatedHistory, error) {
	if method == todo.CreateWorkflowInstanceMethod {
		return workflowacl.WorkflowNameFromCreateRequest(data)
	}

	if preLoadedMeta != nil {
		return preLoadedMeta.GetName(), nil, nil
	}

	_, ometa, err := o.loadInternalState(ctx)
	if err != nil {
		return "", nil, err
	}
	if ometa == nil {
		return "", nil, nil
	}
	return ometa.GetName(), nil, nil
}
