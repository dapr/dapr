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
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

const workflowACLDeniedMsg = "access denied by workflow access policy"

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

	// Self-calls are exempt: the policy is a cross-app gate.
	callerAppID := workflowacl.CallerAppID(md)
	if callerAppID == o.appID {
		return nil
	}

	operation, err := workflowacl.WorkflowOperationFromMethod(method, parsedAddEvent)
	if err != nil {
		log.Warnf("Workflow actor '%s': workflow access policy denied call '%s': could not derive operation from request: %v", o.actorID, method, err)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(workflowacl.OperationTypeWorkflow), method)
		return status.Errorf(codes.PermissionDenied, "%s: malformed request for method '%s'", workflowACLDeniedMsg, method)
	}
	if operation == "" {
		// Non-subject methods (reminders, internal protocol) are only valid
		// from the local daprd. Cross-app callers cannot invoke them.
		log.Warnf("Workflow actor '%s': workflow access policy denied cross-app call to non-subject method '%s' from app '%s'", o.actorID, method, callerAppID)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(workflowacl.OperationTypeWorkflow), method)
		return status.Errorf(codes.PermissionDenied, "%s: app '%s' cannot invoke method '%s'", workflowACLDeniedMsg, callerAppID, method)
	}

	if callerAppID == "" {
		log.Warnf("Workflow actor '%s': workflow access policy denied call '%s' with missing caller identity", o.actorID, method)
		diag.DefaultMonitoring.WorkflowACLActionDenied("", string(workflowacl.OperationTypeWorkflow), string(operation))
		return status.Errorf(codes.PermissionDenied, "%s: caller identity missing on workflow '%s' operation", workflowACLDeniedMsg, operation)
	}

	name, err := o.workflowNameForOperation(ctx, method, data, preLoadedMeta)
	if err != nil {
		log.Errorf("Workflow actor '%s': failed to resolve workflow name for policy check on '%s': %v", o.actorID, method, err)
		return status.Error(codes.Internal, "failed to evaluate workflow access policy")
	}

	if !policies.Evaluate(callerAppID, workflowacl.OperationTypeWorkflow, operation, name) {
		log.Warnf("Workflow actor '%s': workflow access policy denied app '%s' operation '%s' on '%s'", o.actorID, callerAppID, operation, name)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(workflowacl.OperationTypeWorkflow), string(operation))
		return status.Errorf(codes.PermissionDenied, "%s: app '%s' operation '%s' on workflow '%s' (instance '%s')", workflowACLDeniedMsg, callerAppID, operation, name, o.actorID)
	}

	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, string(workflowacl.OperationTypeWorkflow), string(operation))
	return nil
}

func (o *orchestrator) workflowNameForOperation(ctx context.Context, method string, data []byte, preLoadedMeta *backend.WorkflowMetadata) (string, error) {
	if method == todo.CreateWorkflowInstanceMethod {
		return workflowacl.WorkflowNameFromCreateRequest(data)
	}

	if preLoadedMeta != nil {
		return preLoadedMeta.GetName(), nil
	}

	_, ometa, err := o.loadInternalState(ctx)
	if err != nil {
		return "", err
	}
	if ometa == nil {
		return "", nil
	}
	return ometa.GetName(), nil
}
