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

package runtime

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	actorrouter "github.com/dapr/dapr/pkg/actors/router"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/internal/loader"
	"github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/internal/loader/kubernetes"
	"github.com/dapr/dapr/pkg/internal/loader/validate"
	"github.com/dapr/dapr/pkg/modes"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func (a *DaprRuntime) workflowAccessPolicyLoader() loader.Loader[wfaclapi.WorkflowAccessPolicy] {
	switch a.runtimeConfig.mode {
	case modes.KubernetesMode:
		return kubernetes.NewWorkflowAccessPolicies(kubernetes.Options{
			Config:    a.runtimeConfig.kubernetes,
			Client:    a.operatorClient,
			Namespace: a.namespace,
		}, a.runtimeConfig.id)
	case modes.StandaloneMode:
		return disk.NewWorkflowAccessPolicies(disk.Options{
			AppID: a.runtimeConfig.id,
			Paths: a.runtimeConfig.standalone.ResourcesPath,
		})
	default:
		return nil
	}
}

func (a *DaprRuntime) loadWorkflowAccessPolicies(ctx context.Context) error {
	l := a.workflowAccessPolicyLoader()
	if l == nil {
		return nil
	}

	policies, err := l.Load(ctx)
	if err != nil {
		return err
	}

	valid := policies[:0]
	for _, p := range policies {
		if err := validate.WorkflowAccessPolicy(ctx, &p); err != nil {
			log.Warnf("WorkflowAccessPolicy %q failed validation, skipping: %s", p.Name, err)
			continue
		}
		a.compStore.AddWorkflowAccessPolicy(p)
		valid = append(valid, p)
	}

	compiled := workflowacl.Compile(valid)
	a.daprGRPCAPI.SetWorkflowAccessPolicies(compiled)

	if compiled != nil {
		log.Infof("Loaded %d workflow access policy resource(s)", len(valid))
	}

	return nil
}

// buildWorkflowACLChecker creates a WorkflowACLChecker for the actor router.
// Remote calls are enforced at the callee's CallActor gRPC handler.
// Same-app calls (callerAppID == this sidecar's appID) bypass the policy.
// WorkflowAccessPolicy is meant to gate cross-app access; an app talking to
// its own actors must not be subject to it.
func (a *DaprRuntime) buildWorkflowACLChecker() actorrouter.WorkflowACLChecker {
	if !a.globalConfig.IsFeatureEnabled(config.WorkflowAccessPolicy) {
		return nil
	}

	return func(callerAppID string, req *internalv1pb.InternalInvokeRequest) error {
		// Same-app self-call. The local actor router only ever passes its
		// own appID as callerAppID, so any local call here is by definition
		// an in-app call.
		if callerAppID == a.runtimeConfig.id {
			return nil
		}

		result, err := workflowacl.EnforceRequest(
			a.daprGRPCAPI.GetWorkflowAccessPolicies(), callerAppID,
			req.GetActor().GetActorType(),
			req.GetMessage().GetMethod(),
			req.GetMessage().GetData().GetValue(),
		)
		if err != nil {
			return status.Errorf(codes.Internal, "workflow access policy: %v", err)
		}
		if result == nil {
			return nil
		}

		if !result.Allowed {
			log.Warnf("Workflow access policy denied app '%s' for %s operation '%s'", callerAppID, result.OpType, result.Operation)
			diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(result.OpType), result.Operation)
			return status.Errorf(codes.PermissionDenied, "access denied by workflow access policy")
		}

		diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, string(result.OpType), result.Operation)
		return nil
	}
}

// warnIfPoliciesExistWithoutFeatureFlag checks whether WorkflowAccessPolicy
// resources exist even though the feature flag is disabled. Helps catch
// misconfigurations where an operator creates policies but forgets to enable
// the WorkflowAccessPolicy feature flag.
func (a *DaprRuntime) warnIfPoliciesExistWithoutFeatureFlag(ctx context.Context) {
	l := a.workflowAccessPolicyLoader()
	if l == nil {
		return
	}

	policies, err := l.Load(ctx)
	if err != nil {
		log.Warnf("Failed to check for WorkflowAccessPolicy resources: %s", err)
		return
	}
	if len(policies) > 0 {
		log.Warnf("Found %d WorkflowAccessPolicy resource(s) but the WorkflowAccessPolicy feature flag is NOT enabled. "+
			"Policies will NOT be enforced. Enable the feature flag in your Dapr configuration to activate enforcement.",
			len(policies))
	}
}
