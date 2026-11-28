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

package secret

import (
	"strings"

	"github.com/dapr/dapr/pkg/runtime/meta"
)

// sensitiveNameParts are substrings that usually mean a metadata field holds
// a credential. Names are compared after lowercasing and removing `_` / `-`.
var sensitiveNameParts = []string{
	"password",
	"passwd",
	"token",
	"apikey",
	"accesskey",
	"privatekey",
	"clientsecret",
	"clientkey",
	"connectionstring",
	"accountkey",
}

// isSensitiveMetadataName reports whether a component metadata field name
// looks like it holds a credential.
func isSensitiveMetadataName(name string) bool {
	n := strings.ToLower(name)
	n = strings.ReplaceAll(n, "_", "")
	n = strings.ReplaceAll(n, "-", "")

	for _, part := range sensitiveNameParts {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

// WarnPlaintextCredentials logs a warning when a resource has a sensitive
// metadata field set as a plaintext value instead of secretKeyRef or envRef.
func WarnPlaintextCredentials(resource meta.Resource) {
	for _, m := range resource.NameValuePairs() {
		if !isSensitiveMetadataName(m.Name) {
			continue
		}
		if strings.TrimSpace(m.Value.String()) == "" {
			continue
		}
		if m.SecretKeyRef.Name != "" || m.EnvRef != "" {
			continue
		}

		log.Warnf(
			"%s %s metadata field %q uses a plaintext value. Use secretKeyRef instead. See https://docs.dapr.io/operations/components/component-secrets/",
			resource.Kind(),
			resource.LogName(),
			m.Name,
		)
	}
}
