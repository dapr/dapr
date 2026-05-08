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
	// Rules defines the allow-list of which callers can perform which operations.
	// +optional
	Rules []WorkflowAccessPolicyRule `json:"rules,omitempty"`
}

// WorkflowAccessPolicyRule grants the listed callers access to the listed
// workflows and/or activities. Presence of a matching rule grants access; if
// no rule matches, the call is denied. At least one of workflows or
// activities must be set.
//
// +kubebuilder:validation:XValidation:rule="(has(self.workflows) && size(self.workflows) > 0) || (has(self.activities) && size(self.activities) > 0)",message="at least one of workflows or activities must contain a rule"
type WorkflowAccessPolicyRule struct {
	// Callers that this rule applies to. The policy is a cross-app gate;
	// listing the target app's own appID here has no effect because
	// same-app calls are always exempt from enforcement.
	// +kubebuilder:validation:MinItems=1
	Callers []WorkflowCaller `json:"callers"`

	// Workflows are the workflow rules that the matched callers are allowed.
	// +optional
	Workflows []WorkflowRule `json:"workflows,omitempty"`

	// Activities are the activity rules that the matched callers are allowed.
	// +optional
	Activities []ActivityRule `json:"activities,omitempty"`
}

// WorkflowCaller identifies a calling application.
type WorkflowCaller struct {
	// AppID is the Dapr app ID of the caller.
	// +kubebuilder:validation:MinLength=1
	AppID string `json:"appID"`
}

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

// WorkflowRule grants the matched callers access to the listed operations on
// workflows whose name matches Name (exact or glob).
type WorkflowRule struct {
	// Name is the exact name or glob pattern for the workflow.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Operations is the set of operations this rule applies to.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Enum=schedule;terminate;raise;pause;resume;purge;get;rerun
	// +listType=set
	Operations []WorkflowOperation `json:"operations"`

	// Requires is a list of history events that must all be present in the
	// caller's propagated workflow history for this rule to apply. Entries
	// are evaluated as a set — order is not significant. Only meaningful
	// for operations that carry propagated history (currently `schedule`);
	// rules whose `requires` cover other operations will fail-closed
	// because no history is available to satisfy them.
	// +optional
	Requires []RequiredEvent `json:"requires,omitempty"`
}

// ActivityRule grants the matched callers access to schedule the activity
// whose name matches Name (exact or glob). Activities only have one
// operation (schedule).
type ActivityRule struct {
	// Name is the exact name or glob pattern for the activity.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Requires is a list of history events that must all be present in the
	// caller's propagated workflow history for this rule to apply. Entries
	// are evaluated as a set — order is not significant.
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
// RequiredEvent. Started/Completed apply to eventType=activity and
// eventType=workflow; Raised is the only valid status for eventType=event.
type RequiredStatus string

const (
	RequiredStatusStarted   RequiredStatus = "Started"
	RequiredStatusCompleted RequiredStatus = "Completed"
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
	// +kubebuilder:validation:Enum=Started;Completed;Raised
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
