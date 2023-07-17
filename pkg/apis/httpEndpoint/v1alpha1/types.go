/*
Copyright 2023 The Dapr Authors
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
)

//+genclient
//+genclient:noStatus
//+kubebuilder:object:root=true

// HTTPEndpoint describes a Dapr HTTPEndpoint type for external service invocation.
// This endpoint can be external to Dapr, or external to the environment.
type HTTPEndpoint struct {
	metav1.TypeMeta `json:",inline"`
	//+optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	//+optional
	Spec HTTPEndpointSpec `json:"spec,omitempty"`
	//+optional
	Auth          `json:"auth,omitempty"`
	common.Scoped `json:",inline"`
}

const kind = "HTTPEndpoint"

// Kind returns the component kind.
func (HTTPEndpoint) Kind() string {
	return kind
}

// GetSecretStore returns the name of the secret store.
func (h HTTPEndpoint) GetSecretStore() string {
	return h.Auth.SecretStore
}

// NameValuePairs returns the component's headers as name/value pairs
func (h HTTPEndpoint) NameValuePairs() []common.NameValuePair {
	return h.Spec.Headers
}

// TLSField describes tls specific properties for endpoint authenticatin
type TLSField struct {
	// Value of the property, in plaintext.
	//+optional
	Value common.DynamicValue `json:"value,omitempty"`
	// SecretKeyRef is the reference of a value in a secret store component.
	//+optional
	SecretKeyRef common.SecretKeyRef `json:"secretKeyRef,omitempty"`
}

// HTTPEndpointSpec describes an access specification for allowing external service invocations.
type HTTPEndpointSpec struct {
	BaseURL string `json:"baseUrl" validate:"required"`
	//+optional
	Headers []common.NameValuePair `json:"headers"`
	//+optional
	TLSRootCA TLSField `json:"tlsRootCA"`
	//+optional
	TLSClientCert TLSField `json:"tlsClientCert"`
	//+optional
	TLSClientKey TLSField `json:"tlsClientKey"`
	//+optional
	TLSRenegotiation string `json:"tlsRenegotiation"`
}

// Auth represents authentication details for the component.
type Auth struct {
	SecretStore string `json:"secretStore"`
}

//+kubebuilder:object:root=true

// HTTPEndpointList is a list of Dapr HTTPEndpoints.
type HTTPEndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []HTTPEndpoint `json:"items"`
}

type GenericNameValueResource struct {
	Name        string
	Namespace   string
	SecretStore string
	Pairs       []common.NameValuePair
}

func (g GenericNameValueResource) Kind() string {
	return kind
}
func (g GenericNameValueResource) GetName() string {
	return g.Name
}
func (g GenericNameValueResource) GetNamespace() string {
	return g.Namespace
}
func (g GenericNameValueResource) GetSecretStore() string {
	return g.SecretStore
}
func (g GenericNameValueResource) NameValuePairs() []common.NameValuePair {
	return g.Pairs
}
