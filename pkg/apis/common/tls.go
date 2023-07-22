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

package common

// +kubebuilder:object:generate=true

// TLSDocument describes and in-line or pointer to a document to build a TLS configuration.
type TLSDocument struct {
	// Value of the property, in plaintext.
	//+optional
	Value *DynamicValue `json:"value,omitempty"`
	// SecretKeyRef is the reference of a value in a secret store component.
	//+optional
	SecretKeyRef *SecretKeyRef `json:"secretKeyRef,omitempty"`
}

// TLS describes how to build client or server TLS configurations.
type TLS struct {
	//+optional
	RootCA *TLSDocument `json:"rootCA"`
	//+optional
	Certificate *TLSDocument `json:"certificate"`
	//+optional
	PrivateKey *TLSDocument `json:"privateKey"`
	//+optional
	//+kubebuilder:validation:Enum={"Never", "OnceAsClient", "FreelyAsClient"}
	//+kubebuilder:default=Never
	Renegotiation *Renegotiation `json:"renegotiation"`
}

// Renegotiation sets the underlying tls negotiation strategy for an http channel.
type Renegotiation string

const (
	NegotiateNever          Renegotiation = "Never"
	NegotiateOnceAsClient   Renegotiation = "OnceAsClient"
	NegotiateFreelyAsClient Renegotiation = "FreelyAsClient"
)
