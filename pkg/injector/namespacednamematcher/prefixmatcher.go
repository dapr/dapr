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
	"fmt"
	"strings"
)

type namespacedPrefix struct {
	nsValue    string
	nsIsPrefix bool
	saValue    string
	saIsPrefix bool
}

// PrefixMatcher matches namespace:name pairs using exact string comparison or
// prefix matching (indicated by a trailing '*').
type PrefixMatcher struct {
	entries []namespacedPrefix
}

// NewPrefixMatcher parses a CSV string of "namespace:serviceAccountName" pairs
// where either component may end with '*' to indicate prefix matching, and
// returns a PrefixMatcher.
func NewPrefixMatcher(s string) (*PrefixMatcher, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	var entries []namespacedPrefix
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
		sa := strings.TrimSpace(parts[1])

		if ns == "" && sa == "" {
			return nil, errors.New("service account name and namespace cannot both be empty")
		}

		nsValue, nsIsPrefix, err := parsePrefixComponent(ns)
		if err != nil {
			return nil, fmt.Errorf("invalid namespace %q: %w", ns, err)
		}
		saValue, saIsPrefix, err := parsePrefixComponent(sa)
		if err != nil {
			return nil, fmt.Errorf("invalid service account %q: %w", sa, err)
		}

		if nsIsPrefix && nsValue == "" && saIsPrefix && saValue == "" {
			return nil, errors.New("service account name and namespace prefixes cannot both be empty (i.e. '*:*')")
		}

		entries = append(entries, namespacedPrefix{
			nsValue:    nsValue,
			nsIsPrefix: nsIsPrefix,
			saValue:    saValue,
			saIsPrefix: saIsPrefix,
		})
	}

	if len(entries) == 0 {
		return nil, nil
	}
	return &PrefixMatcher{entries: entries}, nil
}

func (m *PrefixMatcher) MatchesNamespacedName(namespace, name string) bool {
	for _, e := range m.entries {
		if matchComponent(namespace, e.nsValue, e.nsIsPrefix) &&
			matchComponent(name, e.saValue, e.saIsPrefix) {
			return true
		}
	}
	return false
}

// parsePrefixComponent extracts the value and whether it is a prefix from a
// string that may end with '*'. The wildcard is only allowed at the very end.
func parsePrefixComponent(s string) (value string, isPrefix bool, err error) {
	idx := strings.Index(s, "*")
	if idx == -1 {
		return s, false, nil
	}
	if idx != len(s)-1 {
		return "", false, fmt.Errorf("wildcard '*' is only allowed at the end of the string, got: %s", s)
	}
	return s[:len(s)-1], true, nil
}

// matchComponent checks whether the input matches the given value, either by
// exact equality or prefix matching.
func matchComponent(input, value string, isPrefix bool) bool {
	if isPrefix {
		return strings.HasPrefix(input, value)
	}
	return input == value
}
