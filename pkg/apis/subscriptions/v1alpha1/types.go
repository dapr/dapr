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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/apis/common"
	"github.com/dapr/dapr/pkg/apis/subscriptions"
)

const (
	Kind    = "Subscription"
	Version = "v1alpha1"
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
	Metadata        map[string]string `json:"metadata,omitempty"`
	Route           string            `json:"route"`
	BulkSubscribe   BulkSubscribe     `json:"bulkSubscribe,omitempty"`
	DeadLetterTopic string            `json:"deadLetterTopic,omitempty"`
}

// BulkSubscribe encapsulates the bulk subscription configuration for a topic.
type BulkSubscribe struct {
	Enabled bool `json:"enabled"`
	// +optional
	MaxMessagesCount   int32 `json:"maxMessagesCount,omitempty"`
	MaxAwaitDurationMs int32 `json:"maxAwaitDurationMs,omitempty"`
}

// +kubebuilder:object:root=true

// SubscriptionList is a list of Dapr event sources.
type SubscriptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Subscription `json:"items"`
}

func (Subscription) Kind() string {
	return Kind
}

func (Subscription) APIVersion() string {
	return subscriptions.GroupName + "/" + Version
}

// EmptyMetaDeepCopy returns a new instance of the subscription type with the
// TypeMeta's Kind and APIVersion fields set.
func (s Subscription) EmptyMetaDeepCopy() metav1.Object {
	n := s.DeepCopy()
	n.TypeMeta = metav1.TypeMeta{
		Kind:       Kind,
		APIVersion: subscriptions.GroupName + "/" + Version,
	}
	n.ObjectMeta = metav1.ObjectMeta{Name: s.Name}
	return n
}

func (s Subscription) GetName() string {
	return s.Name
}

func (s Subscription) GetNamespace() string {
	return s.Namespace
}

func (s Subscription) GetSecretStore() string {
	return ""
}

func (s Subscription) LogName() string {
	return s.GetName()
}

func (s Subscription) NameValuePairs() []common.NameValuePair {
	return nil
}

func (s Subscription) ClientObject() client.Object {
	return &s
}

func (s Subscription) GetScopes() []string {
	return s.Scopes
}
