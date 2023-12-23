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
	httpendpoint "github.com/dapr/dapr/pkg/apis/httpEndpoint"
)

const (
	Kind    = "HTTPEndpoint"
	Version = "v1alpha1"
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

// GetName returns the component name.
func (h HTTPEndpoint) GetName() string {
	return h.Name
}

// GetNamespace returns the component namespace.
func (h HTTPEndpoint) GetNamespace() string {
	return h.Namespace
}

// GetSecretStore returns the name of the secret store.
func (h HTTPEndpoint) GetSecretStore() string {
	return h.Auth.SecretStore
}

// LogName returns the name of the component that can be used in logging.
func (h HTTPEndpoint) LogName() string {
	return h.Name + " (" + h.Spec.BaseURL + ")"
}

// NameValuePairs returns the component's headers as name/value pairs
func (h HTTPEndpoint) NameValuePairs() []common.NameValuePair {
	return h.Spec.Headers
}

// nil returns a bool indicating if a tls private key has a secret reference
func (h HTTPEndpoint) HasTLSPrivateKeySecret() bool {
	return h.Spec.ClientTLS != nil && h.Spec.ClientTLS.PrivateKey != nil && h.Spec.ClientTLS.PrivateKey.SecretKeyRef != nil && h.Spec.ClientTLS.PrivateKey.SecretKeyRef.Name != ""
}

// HasTLSClientCertSecret returns a bool indicating if a tls client cert has a secret reference
func (h HTTPEndpoint) HasTLSClientCertSecret() bool {
	return h.Spec.ClientTLS != nil && h.Spec.ClientTLS.Certificate != nil && h.Spec.ClientTLS.Certificate.SecretKeyRef != nil && h.Spec.ClientTLS.Certificate.SecretKeyRef.Name != ""
}

// HasTLSRootCASecret returns a bool indicating if a tls root cert has a secret reference
func (h HTTPEndpoint) HasTLSRootCASecret() bool {
	return h.Spec.ClientTLS != nil && h.Spec.ClientTLS.RootCA != nil && h.Spec.ClientTLS.RootCA.SecretKeyRef != nil && h.Spec.ClientTLS.RootCA.SecretKeyRef.Name != ""
}

// HasTLSRootCA returns a bool indicating if the HTTP endpoint contains a tls root ca
func (h HTTPEndpoint) HasTLSRootCA() bool {
	return h.Spec.ClientTLS != nil && h.Spec.ClientTLS.RootCA != nil && h.Spec.ClientTLS.RootCA.Value != nil
}

// HasTLSClientCert returns a bool indicating if the HTTP endpoint contains a tls client cert
func (h HTTPEndpoint) HasTLSClientCert() bool {
	return h.Spec.ClientTLS != nil && h.Spec.ClientTLS.Certificate != nil && h.Spec.ClientTLS.Certificate.Value != nil
}

// HasTLSClientKey returns a bool indicating if the HTTP endpoint contains a tls client key
func (h HTTPEndpoint) HasTLSPrivateKey() bool {
	return h.Spec.ClientTLS != nil && h.Spec.ClientTLS.PrivateKey != nil && h.Spec.ClientTLS.PrivateKey.Value != nil
}

// EmptyMetaDeepCopy returns a new instance of the component type with the
// TypeMeta's Kind and APIVersion fields set.
func (h HTTPEndpoint) EmptyMetaDeepCopy() metav1.Object {
	n := h.DeepCopy()
	n.TypeMeta = metav1.TypeMeta{
		Kind:       Kind,
		APIVersion: httpendpoint.GroupName + "/" + Version,
	}
	n.ObjectMeta = metav1.ObjectMeta{Name: h.Name}
	return n
}

// HTTPEndpointSpec describes an access specification for allowing external service invocations.
type HTTPEndpointSpec struct {
	BaseURL string `json:"baseUrl" validate:"required"`
	//+optional
	Headers []common.NameValuePair `json:"headers"`
	//+optional
	ClientTLS *common.TLS `json:"clientTLS,omitempty"`
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
