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

// WorkflowAccessPolicyRule defines a set of callers and the operations they
// are allowed or denied.
type WorkflowAccessPolicyRule struct {
	// Callers that this rule applies to.
	// +kubebuilder:validation:MinItems=1
	Callers []WorkflowCaller `json:"callers"`

	// Operations that the matched callers are allowed/denied to perform.
	// +kubebuilder:validation:MinItems=1
	Operations []WorkflowOperationRule `json:"operations"`
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

// WorkflowOperationType is the type of operation: "workflow" or "activity".
type WorkflowOperationType string

const (
	WorkflowOperationTypeWorkflow WorkflowOperationType = "workflow"
	WorkflowOperationTypeActivity WorkflowOperationType = "activity"
)

// WorkflowOperation is the specific operation being controlled (e.g., "schedule").
type WorkflowOperation string

const (
	WorkflowOperationSchedule WorkflowOperation = "schedule"
)

// WorkflowOperationRule defines access control for a specific workflow or activity operation.
type WorkflowOperationRule struct {
	// Type is "workflow" or "activity".
	// +kubebuilder:validation:Enum=workflow;activity
	Type WorkflowOperationType `json:"type"`

	// Name is the exact name or glob pattern for the workflow/activity.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Operation defaults to "schedule" if omitted.
	// +optional
	// +kubebuilder:validation:Enum=schedule
	Operation *WorkflowOperation `json:"operation,omitempty"`

	// Action is "allow" or "deny".
	// +kubebuilder:validation:Enum=allow;deny
	Action PolicyAction `json:"action"`

	// Requires is a list of history events that must all be present in the
	// caller's propagated workflow history for this rule to apply. When set,
	// the rule only matches if every entry can be located in the caller's
	// history. When the field is omitted/empty, means nothing is required.
	// +optional
	Requires []RequiredEvent `json:"requires,omitempty"`
}

// RequiredStatus is the lifecycle phase of a history event referenced by a
// RequiredEvent. Started, Completed and Failed apply uniformly to activities,
// child workflows, and the workflow's own execution. The category is inferred
// from which *Name field is set on the enclosing RequiredEvent. Raised
// matches external events.
type RequiredStatus string

const (
	RequiredStatusStarted   RequiredStatus = "Started"
	RequiredStatusCompleted RequiredStatus = "Completed"
	RequiredStatusFailed    RequiredStatus = "Failed"
	RequiredStatusRaised    RequiredStatus = "Raised"
)

// RequiredEvent is a single entry in a rule's Requires list. It matches when
// the caller's propagated history contains an event with the given Status
// produced by the named activity / workflow / external event. Exactly one
// of activityName, workflowName, or eventName must be set.
//
// +kubebuilder:validation:XValidation:rule="(has(self.activityName) ? 1 : 0) + (has(self.workflowName) ? 1 : 0) + (has(self.eventName) ? 1 : 0) == 1",message="exactly one of activityName, workflowName, or eventName must be set"
type RequiredEvent struct {
	// +kubebuilder:validation:Enum=Started;Completed;Failed;Raised
	Status RequiredStatus `json:"status"`

	// ActivityName restricts the match to events produced by the named activity.
	// +optional
	ActivityName string `json:"activityName,omitempty"`

	// WorkflowName restricts the match to events produced by the named workflow
	// (own or child).
	// +optional
	WorkflowName string `json:"workflowName,omitempty"`

	// EventName restricts the match to an external event with the given name.
	// Only valid with Status=Raised.
	// +optional
	EventName string `json:"eventName,omitempty"`

	// AppID restricts the match to events produced by the named app.
	// +optional
	AppID string `json:"appID,omitempty"`
}

// +kubebuilder:object:root=true

// WorkflowAccessPolicyList is a list of WorkflowAccessPolicy resources.
type WorkflowAccessPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []WorkflowAccessPolicy `json:"items"`
}
