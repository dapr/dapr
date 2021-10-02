// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true

// Subscription describes an pub/sub event subscription.
type Subscription struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SubscriptionSpec `json:"spec,omitempty"`
	// +optional
	Scopes []string `json:"scopes,omitempty"`
}

// SubscriptionSpec is the spec for an event subscription.
type SubscriptionSpec struct {
	Topic      string `json:"topic"`
	Pubsubname string `json:"pubsubname"`
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`
	Route    string            `json:"route"`
}

// +kubebuilder:object:root=true

// SubscriptionList is a list of Dapr event sources.
type SubscriptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Subscription `json:"items"`
}
