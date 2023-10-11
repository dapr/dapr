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

import (
	"strconv"

	apiextensionsV1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// NameValuePair is a name/value pair.
type NameValuePair struct {
	// Name of the property.
	Name string `json:"name"`
	// Value of the property, in plaintext.
	//+optional
	Value DynamicValue `json:"value,omitempty"`
	// SecretKeyRef is the reference of a value in a secret store component.
	//+optional
	SecretKeyRef SecretKeyRef `json:"secretKeyRef,omitempty"`
	// EnvRef is the name of an environmental variable to read the value from.
	//+optional
	EnvRef string `json:"envRef,omitempty"`
}

// HasValue returns true if the NameValuePair has a non-empty value.
func (nvp NameValuePair) HasValue() bool {
	return len(nvp.Value.JSON.Raw) > 0
}

// SetValue sets the value.
func (nvp *NameValuePair) SetValue(val []byte) {
	nvp.Value = DynamicValue{
		JSON: apiextensionsV1.JSON{
			Raw: val,
		},
	}
}

// SecretKeyRef is a reference to a secret holding the value for the name/value item.
type SecretKeyRef struct {
	// Secret name.
	Name string `json:"name"`
	// Field in the secret.
	//+optional
	Key string `json:"key"`
}

// DynamicValue is a dynamic value struct for the component.metadata pair value.
// +kubebuilder:validation:Type=""
// +kubebuilder:validation:Schemaless
type DynamicValue struct {
	apiextensionsV1.JSON `json:",inline"`
}

// String returns the string representation of the raw value.
// If the value is a string, it will be unquoted as the string is guaranteed to be a JSON serialized string.
func (d *DynamicValue) String() string {
	s := string(d.Raw)
	c, err := strconv.Unquote(s)
	if err == nil {
		s = c
	}
	return s
}
