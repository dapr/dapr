/*
Copyright 2021 The Dapr Authors
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

package v2alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true
// +kubebuilder:storageversion

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
	// The PubSub component name.
	Pubsubname string `json:"pubsubname"`
	// The topic name to subscribe to.
	Topic string `json:"topic"`
	// The optional metadata to provide the subscription.
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`
	// The Routes configuration for this topic.
	Routes Routes `json:"routes"`
	// The optional dead letter queue for this topic to send events to.
	DeadLetterTopic string `json:"deadLetterTopic,omitempty"`
	// The option to enable bulk subscription for this topic.
	BulkSubscribe BulkSubscribe `json:"bulkSubscribe,omitempty"`
}

// BulkSubscribe encapsulates the bulk subscription configuration for a topic.
type BulkSubscribe struct {
	Enabled bool `json:"enabled"`
	// +optional
	MaxMessagesCount   int32 `json:"maxMessagesCount,omitempty"`
	MaxAwaitDurationMs int32 `json:"maxAwaitDurationMs,omitempty"`
}

// Routes encapsulates the rules and optional default path for a topic.
type Routes struct {
	// The list of rules for this topic.
	// +optional
	Rules []Rule `json:"rules,omitempty"`
	// The default path for this topic.
	// +optional
	Default string `json:"default,omitempty"`
}

// Rule is used to specify the condition for sending
// a message to a specific path.
type Rule struct {
	// The optional CEL expression used to match the event.
	// If the match is not specified, then the route is considered
	// the default. The rules are tested in the order specified,
	// so they should be define from most-to-least specific.
	// The default route should appear last in the list.
	Match string `json:"match"`

	// The path for events that match this rule.
	Path string `json:"path"`
}

// +kubebuilder:object:root=true

// SubscriptionList is a list of Dapr event sources.
type SubscriptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Subscription `json:"items"`
}
