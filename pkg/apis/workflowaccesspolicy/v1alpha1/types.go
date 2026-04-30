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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/apis/common"
	"github.com/dapr/dapr/pkg/apis/workflowaccesspolicy"
)

const kind = "WorkflowAccessPolicy"

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true

// WorkflowAccessPolicy controls which app IDs are permitted to schedule
// specific workflows and activities on a target application.
//
//nolint:recvcheck
type WorkflowAccessPolicy struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec WorkflowAccessPolicySpec `json:"spec,omitempty"`

	common.Scoped `json:",inline"`
}

// Kind returns the resource kind.
func (WorkflowAccessPolicy) Kind() string {
	return kind
}

// APIVersion returns the group/version string.
func (WorkflowAccessPolicy) APIVersion() string {
	return workflowaccesspolicy.GroupName + "/v1alpha1"
}

// GetName returns the policy name.
func (w WorkflowAccessPolicy) GetName() string {
	return w.Name
}

// GetNamespace returns the policy namespace.
func (w WorkflowAccessPolicy) GetNamespace() string {
	return w.Namespace
}

// LogName returns a display name suitable for logging.
func (w WorkflowAccessPolicy) LogName() string {
	return w.Name
}

// GetSecretStore returns an empty string — WorkflowAccessPolicy has no secrets.
func (WorkflowAccessPolicy) GetSecretStore() string {
	return ""
}

// GetScopes returns the scopes for this resource.
func (w WorkflowAccessPolicy) GetScopes() []string {
	return w.Scopes
}

// NameValuePairs returns nil — WorkflowAccessPolicy has no name/value pairs.
func (WorkflowAccessPolicy) NameValuePairs() []common.NameValuePair {
	return nil
}

// ClientObject returns the WorkflowAccessPolicy as a controller-runtime client.Object.
func (w WorkflowAccessPolicy) ClientObject() client.Object {
	return &w
}

// EmptyMetaDeepCopy returns a deep copy with only TypeMeta and minimal ObjectMeta.
func (w WorkflowAccessPolicy) EmptyMetaDeepCopy() metav1.Object {
	n := w.DeepCopy()
	n.TypeMeta = metav1.TypeMeta{
		Kind:       kind,
		APIVersion: workflowaccesspolicy.GroupName + "/v1alpha1",
	}
	n.ObjectMeta = metav1.ObjectMeta{
		Name:      w.Name,
		Namespace: w.Namespace,
	}
	return n
}

// WorkflowAccessPolicySpec defines the desired state of WorkflowAccessPolicy.
type WorkflowAccessPolicySpec struct {
	// DefaultAction is the action when no rule matches. Defaults to "deny" if omitted.
	// +optional
	// +kubebuilder:default="deny"
	// +kubebuilder:validation:Enum=allow;deny
	DefaultAction PolicyAction `json:"defaultAction,omitempty"`

	// Rules defines ingress rules for which callers can perform which operations.
	// +optional
	Rules []WorkflowAccessPolicyRule `json:"rules,omitempty"`
}

// WorkflowAccessPolicyRule defines a set of callers and the workflow and/or
// activity rules they are allowed or denied. At least one of workflows or
// activities must be set.
//
// +kubebuilder:validation:XValidation:rule="(has(self.workflows) && size(self.workflows) > 0) || (has(self.activities) && size(self.activities) > 0)",message="at least one of workflows or activities must contain a rule"
type WorkflowAccessPolicyRule struct {
	// Callers that this rule applies to.
	// +kubebuilder:validation:MinItems=1
	Callers []WorkflowCaller `json:"callers"`

	// Workflows are the workflow rules that the matched callers are allowed/denied.
	// +optional
	Workflows []WorkflowRule `json:"workflows,omitempty"`

	// Activities are the activity rules that the matched callers are allowed/denied.
	// +optional
	Activities []ActivityRule `json:"activities,omitempty"`
}

// WorkflowCaller identifies a calling application.
type WorkflowCaller struct {
	// AppID is the Dapr app ID of the caller.
	// +kubebuilder:validation:MinLength=1
	AppID string `json:"appID"`
}

// PolicyAction is the action to take: "allow" or "deny".
type PolicyAction string

const (
	PolicyActionAllow PolicyAction = "allow"
	PolicyActionDeny  PolicyAction = "deny"
)

// WorkflowOperation is the specific workflow operation being controlled.
type WorkflowOperation string

const (
	WorkflowOperationSchedule  WorkflowOperation = "schedule"
	WorkflowOperationTerminate WorkflowOperation = "terminate"
	WorkflowOperationRaise     WorkflowOperation = "raise"
	WorkflowOperationPause     WorkflowOperation = "pause"
	WorkflowOperationResume    WorkflowOperation = "resume"
	WorkflowOperationPurge     WorkflowOperation = "purge"
	WorkflowOperationGet       WorkflowOperation = "get"
	WorkflowOperationRerun     WorkflowOperation = "rerun"
)

// WorkflowRule defines access control for a workflow operation.
type WorkflowRule struct {
	// Name is the exact name or glob pattern for the workflow.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Operations is the set of operations this rule applies to.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Enum=schedule;terminate;raise;pause;resume;purge;get;rerun
	// +listType=set
	Operations []WorkflowOperation `json:"operations"`

	// Action is "allow" or "deny".
	// +kubebuilder:validation:Enum=allow;deny
	Action PolicyAction `json:"action"`
}

// ActivityRule defines access control for an activity. Activities only have a
// single operation (schedule), so there is no operation field.
type ActivityRule struct {
	// Name is the exact name or glob pattern for the activity.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Action is "allow" or "deny".
	// +kubebuilder:validation:Enum=allow;deny
	Action PolicyAction `json:"action"`
}

// +kubebuilder:object:root=true

// WorkflowAccessPolicyList is a list of WorkflowAccessPolicy resources.
type WorkflowAccessPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []WorkflowAccessPolicy `json:"items"`
}
