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
	// caller's propagated workflow history for this rule to apply. Entries
	// are evaluated as a set — the order of entries is not significant, and
	// the events themselves need not appear in any particular order in the
	// caller's history. When the field is omitted/empty, no requirements
	// apply.
	// +optional
	Requires []RequiredEvent `json:"requires,omitempty"`
}

// RequiredEventType is the category of history event a RequiredEvent matches.
type RequiredEventType string

const (
	RequiredEventTypeActivity RequiredEventType = "activity"
	RequiredEventTypeWorkflow RequiredEventType = "workflow"
	RequiredEventTypeEvent    RequiredEventType = "event"
)

// RequiredStatus is the lifecycle phase of a history event referenced by a
// RequiredEvent. Started/Completed/Failed apply to eventType=activity and
// eventType=workflow; Raised is the only valid status for eventType=event.
type RequiredStatus string

const (
	RequiredStatusStarted   RequiredStatus = "Started"
	RequiredStatusCompleted RequiredStatus = "Completed"
	RequiredStatusFailed    RequiredStatus = "Failed"
	RequiredStatusRaised    RequiredStatus = "Raised"
)

// RequiredEvent is a single entry in a rule's Requires list. It matches when
// the caller's propagated history contains an event of the given EventType
// and Status with the given Name. eventType=event must be paired with
// status=Raised; eventType=activity/workflow forbids status=Raised.
//
// +kubebuilder:validation:XValidation:rule="self.eventType == 'event' ? self.status == 'Raised' : self.status != 'Raised'",message="eventType=event requires status=Raised; eventType=activity|workflow forbids status=Raised"
type RequiredEvent struct {
	// EventType is the category of history event to match.
	// +kubebuilder:validation:Enum=activity;workflow;event
	EventType RequiredEventType `json:"eventType"`

	// Status is the lifecycle phase the matched event must have.
	// +kubebuilder:validation:Enum=Started;Completed;Failed;Raised
	Status RequiredStatus `json:"status"`

	// Name is the activity/workflow name (eventType=activity|workflow) or
	// the external event name (eventType=event).
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// AppID restricts the match to events produced by the named app.
	// +optional
	AppID *string `json:"appID,omitempty"`
}

// +kubebuilder:object:root=true

// WorkflowAccessPolicyList is a list of WorkflowAccessPolicy resources.
type WorkflowAccessPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []WorkflowAccessPolicy `json:"items"`
}
