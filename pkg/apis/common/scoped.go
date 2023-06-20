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

// Scoped is a base struct for components and other resources that can be scoped to apps.
type Scoped struct {
	//+optional
	Scopes []string `json:"scopes,omitempty"`
}

// IsAppScoped returns true if the appID is allowed in the scopes for the resource.
func (s Scoped) IsAppScoped(appID string) bool {
	if len(s.Scopes) == 0 {
		// If there are no scopes, then every app is allowed
		return true
	}

	for _, s := range s.Scopes {
		if s == appID {
			return true
		}
	}

	return false
}
