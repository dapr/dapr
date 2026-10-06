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

// Per-operation enforcement happens inside the actor itself (orchestrator /
// activity targets) so the workflow name is resolved against locked state
// without a TOCTOU race. This handler only authenticates the caller and
// stamps identity for the in-actor check.
func (a *api) callActorValidateWorkflowACL(ctx context.Context, in *internalv1pb.InternalInvokeRequest) error {
	if _, isWorkflowOrActivityActor := workflowacl.ParseActorType(in.GetActor().GetActorType()); !isWorkflowOrActivityActor {
		return a.validateSameAppInternalActor(ctx, in.GetActor().GetActorType(), "invoke")
	}

	policies := a.workflowAccessPolicies.Load()
	callerAppID, callerNamespace, err := a.extractCallerIdentity(ctx)
	if err != nil {
		// Identity extraction only fails if mTLS is missing. If there are
		// no policies, allow the call (backward compatible).
		if policies == nil {
			return nil
		}
		return err
	}

	if policies == nil {
		return nil
	}

	if nsErr := a.checkNamespace(callerNamespace); nsErr != nil {
		return nsErr
	}

	workflowacl.SetCallerIdentity(in, callerAppID, callerNamespace)
	return nil
}

// Reminders are scheduled internally by the workflow engine on the same
// daprd that owns the actor. Cross-app callers cannot legitimately reach
// this endpoint; when policies are loaded, only the local daprd is
// permitted to invoke workflow/activity reminders.
func (a *api) callActorReminderValidateWorkflowACL(ctx context.Context, in *internalv1pb.Reminder) error {
	if _, isWorkflowOrActivityActor := workflowacl.ParseActorType(in.GetActorType()); !isWorkflowOrActivityActor {
		return a.validateSameAppInternalActor(ctx, in.GetActorType(), "reminder")
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

	if callerAppID != a.AppID() {
		a.logger.Warnf("Workflow access policy denied cross-app reminder invocation from app '%s'", callerAppID)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, "reminder", "invoke")
		return status.Errorf(codes.PermissionDenied, workflowacl.DeniedMessageBase)
	}

	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, "reminder", "invoke")
	return nil
}

// Reserved internal actor types other than workflow and activity (executor,
// retentioner) are only ever called by daprds of the same app, so callers
// from another app or namespace are denied whether or not policies are
// loaded. Without mTLS there is no caller identity to check.
func (a *api) validateSameAppInternalActor(ctx context.Context, actorType, operation string) error {
	if !workflowacl.IsInternalActorType(actorType) {
		return nil
	}

	if _, ok, err := spiffe.FromGRPCContext(ctx); err == nil && !ok {
		return nil
	}

	callerAppID, callerNamespace, err := a.extractCallerIdentity(ctx)
	if err != nil {
		return err
	}

	if nsErr := a.checkNamespace(callerNamespace); nsErr != nil {
		return nsErr
	}

	if callerAppID != a.AppID() {
		a.logger.Warnf("Workflow access policy denied cross-app call to internal actor type '%s' from app '%s'", actorType, callerAppID)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, "internal", operation)
		return status.Errorf(codes.PermissionDenied, workflowacl.DeniedMessageBase)
	}

	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, "internal", operation)
	return nil
}

// extractCallerIdentity extracts the caller's app ID and namespace from the
// SPIFFE ID in the mTLS peer certificate.
func (a *api) extractCallerIdentity(ctx context.Context) (appID, namespace string, err error) {
	spiffeID, ok, err := spiffe.FromGRPCContext(ctx)
	if err != nil {
		a.logger.Errorf("Workflow access policy failed to extract caller identity: %v", err)
		return "", "", status.Error(codes.Internal, "workflow access policy: failed to extract caller identity")
	}
	if !ok {
		return "", "", status.Error(codes.PermissionDenied, workflowacl.DeniedMessageBase)
	}

	return spiffeID.AppID(), spiffeID.Namespace(), nil
}

// checkNamespace denies calls from a namespace other than this daprd's.
func (a *api) checkNamespace(callerNamespace string) error {
	if callerNamespace != "" && callerNamespace != a.Namespace() {
		a.logger.Warnf("Workflow access policy denied cross-namespace call (caller namespace '%s' != target namespace '%s')", callerNamespace, a.Namespace())
		return status.Errorf(codes.PermissionDenied, workflowacl.DeniedMessageBase)
	}
	return nil
}
