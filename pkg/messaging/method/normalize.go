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

package method

import (
	"fmt"
	"path"
	"strings"
)

// ValidateName checks that a name (e.g. reminder or timer name) does not
// contain characters that could cause path traversal or injection when the
// name is embedded in a URL path. Unlike NormalizeMethod, this rejects any
// name containing '/' or '\' since names are identifiers, not paths.
func ValidateName(name string) error {
	if strings.ContainsAny(name, "#?\x00/\\") {
		return fmt.Errorf("name contains forbidden character: %q", name)
	}
	for i := range name {
		b := name[i]
		if b < 0x20 || b == 0x7f {
			return fmt.Errorf("name contains control character at position %d: %q", i, name)
		}
	}
	if name == "." || name == ".." {
		return fmt.Errorf("name is a path traversal sequence: %q", name)
	}
	return nil
}

// NormalizeMethod validates and cleans a service invocation method name.
// It rejects methods containing '#', '?', null bytes, or control characters
// (bytes 0x01-0x1f and 0x7f), then resolves path traversal via path.Clean.
// The caller is responsible for percent-decoding (for HTTP) before calling.
func NormalizeMethod(method string) (string, error) {
	if strings.ContainsAny(method, "#?\x00") {
		return "", fmt.Errorf("method contains forbidden character: %q", method)
	}

	// Reject control characters (0x01-0x1f and 0x7f DEL).
	for i := range method {
		b := method[i]
		if b < 0x20 || b == 0x7f {
			return "", fmt.Errorf("method contains control character at position %d: %q", i, method)
		}
	}

	// Resolve path traversal sequences.
	cleaned := path.Clean(method)

	// path.Clean on rootless paths can leave leading "../" — strip them.
	for strings.HasPrefix(cleaned, "../") {
		cleaned = cleaned[3:]
	}
	if cleaned == ".." {
		cleaned = ""
	}
	// path.Clean converts empty to "."
	if cleaned == "." {
		cleaned = ""
	}

	return cleaned, nil
}
