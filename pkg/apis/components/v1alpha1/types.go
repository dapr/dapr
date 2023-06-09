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

	"github.com/dapr/dapr/pkg/apis/shared"
	"github.com/dapr/dapr/utils"
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
	shared.Scoped `json:",inline"`
}

// Kind returns the component kind.
func (Component) Kind() string {
	return "Component"
}

// LogName returns the name of the component that can be used in logging.
func (c Component) LogName() string {
	return utils.ComponentLogName(c.ObjectMeta.Name, c.Spec.Type, c.Spec.Version)
}

// ComponentSpec is the spec for a component.
type ComponentSpec struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	//+optional
	IgnoreErrors bool                   `json:"ignoreErrors"`
	Metadata     []shared.NameValuePair `json:"metadata"`
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
