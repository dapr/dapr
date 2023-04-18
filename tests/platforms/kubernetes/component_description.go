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

package kubernetes

// SecretRef is a reference to a secret
type SecretRef struct {
	Name string
	Key  string
}

// MetadataValue is either a raw string value or a secret ref
type MetadataValue struct {
	Raw           string
	FromSecretRef *SecretRef
}

// ComponentDescription holds dapr component description.
type ComponentDescription struct {
	// Name is the name of dapr component
	Name string
	// Namespace to deploy the component to
	Namespace *string
	// Type contains component types (<type>.<component_name>)
	TypeName string
	// MetaData contains the metadata for dapr component
	MetaData map[string]MetadataValue
	// Scopes is the list of target apps that should use this component
	Scopes []string
	// ContainerAsJSON is used for pluggable components
	ContainerAsJSON string
}
