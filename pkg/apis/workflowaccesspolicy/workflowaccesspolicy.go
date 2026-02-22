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
)

// +kubebuilder:validation:Enum=Allow;Deny
type PolicyAction string

const (
	PolicyActionAllow PolicyAction = "Allow"
	PolicyActionDeny  PolicyAction = "Deny"
)

// +kubebuilder:validation:Enum=Activity;Workflow
type WorkflowOperationType string

const (
	WorkflowOperationTypeActivity WorkflowOperationType = "Activity"
	WorkflowOperationTypeWorkflow WorkflowOperationType = "Workflow"
)

// WorkflowAccessPolicyTargetRef selects the protected Dapr application.
// The policy object is namespaced; target namespace is metadata.namespace.
type WorkflowAccessPolicyTargetRef struct {
	// AppID of the Dapr application whose workflow surface is protected by this policy.
	// +kubebuilder:validation:MinLength=1
	AppID string `json:"appID" yaml:"appID"`
}

// WorkflowCallerSelector matches the calling workflow identity.
type WorkflowCallerSelector struct {
	// Namespace of the calling app. If not specified, matches this namespace.
	// +kubebuilder:validation:MinLength=1
	// +optional
	// TODO: @joshvanl
	//Namespace *string `json:"namespace" yaml:"namespace"`

	// AppIDs of the calling app.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MinItems=1
	AppIDs []string `json:"appID" yaml:"appID"`

	// Workflow name (or glob pattern) of the calling workflow.
	// Supports '*' wildcard (glob-style).
	// +kubebuilder:validation:MinLength=1
	Workflow string `json:"workflow" yaml:"workflow"`
}

// WorkflowOperationRule describes an allowed/denied target operation.
type WorkflowOperationRule struct {
	// Type of operation being invoked on the target app.
	// +kubebuilder:validation:Required
	Type WorkflowOperationType `json:"type" yaml:"type"`

	// Name of the target activity or child workflow on the target app.
	// Supports '*' wildcard (glob-style).
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name" yaml:"name"`

	// Action to apply when this operation matches.
	// +kubebuilder:validation:Required
	Action PolicyAction `json:"action" yaml:"action"`
}

// WorkflowAccessPolicyRule binds a caller selector to operation rules.
type WorkflowAccessPolicyRule struct {
	// Caller workflow identity selector.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Callers []WorkflowCallerSelector `json:"callers" yaml:"callers"`

	// Operations authorized for these callers against the target app.
	// Rules are evaluated in-order; first match wins.
	// +kubebuilder:validation:MinItems=1
	Operations []WorkflowOperationRule `json:"operations" yaml:"operations"`
}

// WorkflowAccessPolicySpec defines the desired state of WorkflowAccessPolicy.
type WorkflowAccessPolicySpec struct {
	// TargetRef specifies which Dapr app (in this namespace) the policy applies
	// to.
	// +kubebuilder:validation:Required
	TargetRef WorkflowAccessPolicyTargetRef `json:"targetRef" yaml:"targetRef"`

	// DefaultAction is applied when no rule/operation matches.
	// +kubebuilder:validation:Required
	DefaultAction PolicyAction `json:"defaultAction" yaml:"defaultAction"`

	// Rules evaluated in-order; first matching caller+operation wins.
	// +kubebuilder:validation:MinItems=0
	Rules []WorkflowAccessPolicyRule `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type WorkflowAccessPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec WorkflowAccessPolicySpec `json:"spec" yaml:"spec"`

	// Status is reserved for future use (e.g., observedGeneration, warnings).
	// +optional
	Status metav1.Status `json:"status,omitempty" yaml:"status,omitempty"`
}

// +kubebuilder:object:root=true
type WorkflowAccessPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkflowAccessPolicy `json:"items"`
}
