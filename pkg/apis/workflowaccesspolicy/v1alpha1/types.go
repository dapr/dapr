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
	// +kubebuilder:validation:MaxItems=100
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
	// +kubebuilder:validation:MaxItems=100
	Callers []WorkflowCaller `json:"callers"`

	// Workflows are the workflow rules that the matched callers are allowed.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Workflows []WorkflowRule `json:"workflows,omitempty"`

	// Activities are the activity rules that the matched callers are allowed.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Activities []ActivityRule `json:"activities,omitempty"`
}

// WorkflowCaller identifies a calling application.
type WorkflowCaller struct {
	// AppID is the Dapr app ID of the caller.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
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
// workflows whose name matches Name (exact or glob). An optional `requires`
// gate may be attached, but only when the rule's sole operation is
// `schedule` — schedule is the only operation that carries propagated
// workflow history to check against. Express OR across prerequisite sets by
// listing multiple workflow rules with the same Name, each with its own
// `requires`; access is granted if any matching rule is satisfied.
//
// +kubebuilder:validation:XValidation:rule="!has(self.requires) || size(self.requires) == 0 || (size(self.operations) == 1 && self.operations[0] == 'schedule')",message="requires is only valid when the rule's only operation is 'schedule'"
type WorkflowRule struct {
	// Name is the exact name or glob pattern for the workflow.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`

	// Operations is the set of operations this rule applies to.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:Enum=schedule;terminate;raise;pause;resume;purge;get;rerun
	// +listType=set
	Operations []WorkflowOperation `json:"operations"`

	// Requires is an ordered list of history events that must all be present in
	// the caller's propagated workflow history for this rule to apply. The
	// entries must be satisfied in the order they are listed: each successive
	// entry matches an event at or after the event matched by the previous one.
	// Only valid when the rule's only operation is `schedule`.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Requires []RequiredEvent `json:"requires,omitempty"`
}

// ActivityRule grants the matched callers access to schedule the activity
// whose name matches Name (exact or glob). Activities only have one
// operation (schedule).
type ActivityRule struct {
	// Name is the exact name or glob pattern for the activity.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`

	// Requires is an ordered list of history events that must all be present in
	// the caller's propagated workflow history for this rule to apply. The
	// entries must be satisfied in the order they are listed: each successive
	// entry matches an event at or after the event matched by the previous one.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Requires []RequiredEvent `json:"requires,omitempty"`
}

// RequiredEventType is the history event a RequiredEvent matches. It combines
// the event category with its lifecycle phase. workflow.started matches a
// child-workflow creation (a workflow scheduled by the caller), not the
// caller's own execution.
type RequiredEventType string

const (
	RequiredEventTypeActivityStarted   RequiredEventType = "activity.started"
	RequiredEventTypeActivityCompleted RequiredEventType = "activity.completed"
	RequiredEventTypeWorkflowStarted   RequiredEventType = "workflow.started"
	RequiredEventTypeWorkflowCompleted RequiredEventType = "workflow.completed"
	RequiredEventTypeEventRaised       RequiredEventType = "event.raised"
)

// RequiredEvent is a single entry in a rule's Requires list. It matches when
// the caller's propagated history contains an event of the given EventType,
// produced by AppID, with the given Name.
type RequiredEvent struct {
	// EventType is the history event that must be present.
	// +kubebuilder:validation:Enum=activity.started;activity.completed;workflow.started;workflow.completed;event.raised
	EventType RequiredEventType `json:"eventType"`

	// Name is the activity name (activity.started|activity.completed), the
	// child-workflow name (workflow.started|workflow.completed), or the
	// external event name (event.raised).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Name string `json:"name"`

	// AppID is the app ID that must have produced the matched event. An event
	// only matches when it came from this app's propagated history.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	AppID string `json:"appID"`
}

// +kubebuilder:object:root=true

// WorkflowAccessPolicyList is a list of WorkflowAccessPolicy resources.
type WorkflowAccessPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []WorkflowAccessPolicy `json:"items"`
}
