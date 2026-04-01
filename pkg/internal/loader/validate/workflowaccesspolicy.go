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

package validate

import (
	daprcrds "github.com/dapr/dapr/charts/dapr"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
)

var workflowAccessPolicyValidator = NewValidator("WorkflowAccessPolicy", daprcrds.WorkflowAccessPolicyCRD)

// WorkflowAccessPolicy validates a WorkflowAccessPolicy against the OpenAPI
// schema and CEL rules embedded in the CRD. This provides the same validation
// in standalone mode that the Kubernetes API server provides via CRD admission.
func WorkflowAccessPolicy(policy *wfaclapi.WorkflowAccessPolicy) error {
	return workflowAccessPolicyValidator.Validate(policy)
}
