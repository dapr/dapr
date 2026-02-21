/*
Copyright 2026 The Dapr Authors
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

import "fmt"

// WithNamespaceSuffix appends a suffix to a namespace while respecting Kubernetes DNS label limits.
// The suffix should not include a leading dash.
func WithNamespaceSuffix(namespace, suffix string) string {
	if suffix == "" {
		return namespace
	}
	if namespace == "" {
		return suffix
	}
	out := fmt.Sprintf("%s-%s", namespace, suffix)
	return truncateK8sName(out, 63)
}
