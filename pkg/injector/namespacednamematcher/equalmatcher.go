/*
Copyright 2025 The Dapr Authors
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

package namespacednamematcher

import (
	"errors"
	"strings"
)

type namespacedEqual struct {
	namespace string
	name      string
}

// EqualMatcher matches namespace:name pairs using exact string comparison.
type EqualMatcher struct {
	entries []namespacedEqual
}

// NewEqualMatcher parses a CSV string of "namespace:serviceAccountName" pairs
// and returns an EqualMatcher that performs exact string matching on both
// namespace and name.
func NewEqualMatcher(s string) (*EqualMatcher, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	var entries []namespacedEqual
	for entry := range strings.SplitSeq(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			return nil, errors.New("service account pair not following expected format 'namespace:serviceAccountName'")
		}

		ns := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])

		if ns == "" && name == "" {
			return nil, errors.New("service account name and namespace cannot both be empty")
		}

		entries = append(entries, namespacedEqual{
			namespace: ns,
			name:      name,
		})
	}

	if len(entries) == 0 {
		return nil, nil
	}
	return &EqualMatcher{entries: entries}, nil
}

func (m *EqualMatcher) MatchesNamespacedName(namespace, name string) bool {
	for _, e := range m.entries {
		if e.namespace == namespace && e.name == name {
			return true
		}
	}
	return false
}
