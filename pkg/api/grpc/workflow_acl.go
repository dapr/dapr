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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/security/spiffe"
)

const workflowACLDeniedMsg = "access denied by workflow access policy"

// callActorValidateWorkflowACL checks whether the caller is allowed to invoke
// the target workflow or activity based on WorkflowAccessPolicy resources.
// Returns nil if no policy applies or the call is allowed; returns
// PermissionDenied if denied.
func (a *api) callActorValidateWorkflowACL(ctx context.Context, in *internalv1pb.InternalInvokeRequest) error {
	actorType := in.GetActor().GetActorType()
	opType, isWorkflowActor := workflowacl.ParseActorType(actorType)
	if !isWorkflowActor {
		return nil
	}

	policies := a.workflowAccessPolicies.Load()
	if policies == nil {
		return nil
	}

	method := in.GetMessage().GetMethod()
	data := in.GetMessage().GetData().GetValue()

	callerAppID, callerNamespace, err := a.extractCallerIdentity(ctx)
	if err != nil {
		return err
	}

	if nsErr := a.checkNamespace(callerNamespace); nsErr != nil {
		return nsErr
	}

	opName, subject, err := workflowacl.ExtractOperationName(opType, method, data)
	if err != nil {
		return status.Errorf(codes.Internal, "workflow access policy: failed to extract operation name: %v", err)
	}
	if !subject {
		if !policies.IsCallerKnown(callerAppID) {
			a.logger.Warnf("Workflow access policy denied app '%s' for method '%s' on %s actor (caller not in any allow rule)", callerAppID, method, opType)
			diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(opType), method)
			return status.Errorf(codes.PermissionDenied, workflowACLDeniedMsg)
		}
		diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, string(opType), method)
		return nil
	}

	if !policies.Evaluate(callerAppID, opType, opName) {
		a.logger.Warnf("Workflow access policy denied app '%s' from scheduling %s '%s'", callerAppID, opType, opName)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(opType), "schedule")
		return status.Errorf(codes.PermissionDenied, workflowACLDeniedMsg)
	}

	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, string(opType), "schedule")
	return nil
}

// callActorReminderValidateWorkflowACL checks whether the caller is allowed to
// invoke reminders on a workflow/activity actor. This prevents a malicious
// sidecar from injecting fake activity results or workflow events via the
// CallActorReminder gRPC endpoint.
func (a *api) callActorReminderValidateWorkflowACL(ctx context.Context, in *internalv1pb.Reminder) error {
	actorType := in.GetActorType()
	_, isWorkflowActor := workflowacl.ParseActorType(actorType)
	if !isWorkflowActor {
		return nil
	}

	policies := a.workflowAccessPolicies.Load()
	if policies == nil {
		return nil
	}

	callerAppID, callerNamespace, err := a.extractCallerIdentity(ctx)
	if err != nil {
		return err
	}

	if nsErr := a.checkNamespace(callerNamespace); nsErr != nil {
		return nsErr
	}

	if !policies.IsCallerKnown(callerAppID) {
		a.logger.Warnf("Workflow access policy denied app '%s' from invoking workflow reminders", callerAppID)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, "reminder", "invoke")
		return status.Errorf(codes.PermissionDenied, workflowACLDeniedMsg)
	}

	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, "reminder", "invoke")
	return nil
}

// extractCallerIdentity extracts the caller's app ID and namespace from the
// SPIFFE ID in the mTLS peer certificate.
func (a *api) extractCallerIdentity(ctx context.Context) (appID, namespace string, err error) {
	spiffeID, ok, err := spiffe.FromGRPCContext(ctx)
	if err != nil {
		return "", "", status.Errorf(codes.Internal, "workflow access policy: failed to extract caller identity: %v", err)
	}
	if !ok {
		return "", "", status.Error(codes.PermissionDenied, workflowACLDeniedMsg)
	}

	return spiffeID.AppID(), spiffeID.Namespace(), nil
}

// checkNamespace denies cross-namespace calls when policies are active.
func (a *api) checkNamespace(callerNamespace string) error {
	if callerNamespace != "" && callerNamespace != a.Namespace() {
		a.logger.Warnf("Workflow access policy denied cross-namespace call (caller namespace '%s' != target namespace '%s')", callerNamespace, a.Namespace())
		return status.Errorf(codes.PermissionDenied, workflowACLDeniedMsg)
	}
	return nil
}
