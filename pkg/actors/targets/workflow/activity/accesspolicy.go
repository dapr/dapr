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

package activity

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const workflowACLDeniedMsg = "access denied by workflow access policy"

func (a *activity) checkAccessPolicy(method string, data []byte, md map[string]*internalsv1pb.ListStringValue) error {
	if a.workflowAccessPolicies == nil {
		return nil
	}
	policies := a.workflowAccessPolicies.Load()
	if policies == nil {
		return nil
	}

	name, err := workflowacl.ActivityNameFromExecute(method, data)
	if err != nil {
		log.Warnf("Activity actor '%s': workflow access policy denied call '%s': could not extract name from request: %v", a.actorID, method, err)
		diag.DefaultMonitoring.WorkflowACLActionDenied(workflowacl.CallerAppID(md), string(workflowacl.OperationTypeActivity), method)
		return status.Errorf(codes.PermissionDenied, "%s: malformed request for method '%s'", workflowACLDeniedMsg, method)
	}
	if name == "" {
		return nil
	}

	callerAppID := workflowacl.CallerAppID(md)
	if callerAppID == "" {
		log.Warnf("Activity actor '%s': workflow access policy denied call '%s' with missing caller identity", a.actorID, method)
		diag.DefaultMonitoring.WorkflowACLActionDenied("", string(workflowacl.OperationTypeActivity), string(wfaclapi.WorkflowOperationSchedule))
		return status.Errorf(codes.PermissionDenied, "%s: caller identity missing on activity '%s' schedule", workflowACLDeniedMsg, name)
	}

	if !policies.Evaluate(callerAppID, workflowacl.OperationTypeActivity, wfaclapi.WorkflowOperationSchedule, name) {
		log.Warnf("Activity actor '%s': workflow access policy denied app '%s' on activity '%s'", a.actorID, callerAppID, name)
		diag.DefaultMonitoring.WorkflowACLActionDenied(callerAppID, string(workflowacl.OperationTypeActivity), string(wfaclapi.WorkflowOperationSchedule))
		return status.Errorf(codes.PermissionDenied, "%s: app '%s' schedule on activity '%s'", workflowACLDeniedMsg, callerAppID, name)
	}

	diag.DefaultMonitoring.WorkflowACLActionAllowed(callerAppID, string(workflowacl.OperationTypeActivity), string(wfaclapi.WorkflowOperationSchedule))
	return nil
}
