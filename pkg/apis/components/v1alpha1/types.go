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

	"github.com/dapr/dapr/pkg/apis/common"
	"github.com/dapr/dapr/pkg/apis/components"
	"github.com/dapr/dapr/utils"
)

const (
	Kind    = "Component"
	Version = "v1alpha1"
)

//+genclient
//+genclient:noStatus
//+kubebuilder:object:root=true

// Component describes an Dapr component type.
type Component struct {
	metav1.TypeMeta `json:",inline"`
	//+optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	//+optional
	Spec ComponentSpec `json:"spec,omitempty"`
	//+optional
	Auth          `json:"auth,omitempty"`
	common.Scoped `json:",inline"`
}

// Kind returns the component kind.
func (Component) Kind() string {
	return "Component"
}

// GetName returns the component name.
func (c Component) GetName() string {
	return c.Name
}

// GetNamespace returns the component namespace.
func (c Component) GetNamespace() string {
	return c.Namespace
}

// LogName returns the name of the component that can be used in logging.
func (c Component) LogName() string {
	return utils.ComponentLogName(c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version)
}

// GetSecretStore returns the name of the secret store.
func (c Component) GetSecretStore() string {
	return c.Auth.SecretStore
}

// NameValuePairs returns the component's metadata as name/value pairs
func (c Component) NameValuePairs() []common.NameValuePair {
	return c.Spec.Metadata
}

// EmptyMetaDeepCopy returns a new instance of the component type with the
// TypeMeta's Kind and APIVersion fields set.
func (c Component) EmptyMetaDeepCopy() metav1.Object {
	n := c.DeepCopy()
	n.TypeMeta = metav1.TypeMeta{
		Kind:       Kind,
		APIVersion: components.GroupName + "/" + Version,
	}
	n.ObjectMeta = metav1.ObjectMeta{Name: c.Name}
	return n
}

// ComponentSpec is the spec for a component.
type ComponentSpec struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	//+optional
	IgnoreErrors bool                   `json:"ignoreErrors"`
	Metadata     []common.NameValuePair `json:"metadata"`
	//+optional
	InitTimeout string `json:"initTimeout"`
}

// Auth represents authentication details for the component.
type Auth struct {
	SecretStore string `json:"secretStore"`
}

//+kubebuilder:object:root=true

// ComponentList is a list of Dapr components.
type ComponentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Component `json:"items"`
}
