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
	"path"
	"strings"
)

type namespacedGlob struct {
	nsPattern string
	saPattern string
}

// GlobMatcher matches namespace:name pairs using glob patterns
// (via path.Match). Supports *, ?, and [...] character classes.
type GlobMatcher struct {
	patterns []namespacedGlob
}

// NewGlobMatcher parses a CSV string of "nsPattern:saPattern" pairs and
// returns a GlobMatcher. Each pattern component is validated at parse time.
func NewGlobMatcher(s string) (*GlobMatcher, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	var patterns []namespacedGlob
	for entry := range strings.SplitSeq(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			return nil, errors.New("service account pattern not following expected format 'namespacePattern:serviceAccountPattern'")
		}

		nsPattern := strings.TrimSpace(parts[0])
		saPattern := strings.TrimSpace(parts[1])

		if nsPattern == "" && saPattern == "" {
			return nil, errors.New("service account pattern and namespace pattern cannot both be empty")
		}

		if err := validateGlobPattern(nsPattern); err != nil {
			return nil, fmt.Errorf("invalid namespace pattern %q: %w", nsPattern, err)
		}
		if err := validateGlobPattern(saPattern); err != nil {
			return nil, fmt.Errorf("invalid service account pattern %q: %w", saPattern, err)
		}

		patterns = append(patterns, namespacedGlob{
			nsPattern: nsPattern,
			saPattern: saPattern,
		})
	}

	if len(patterns) == 0 {
		return nil, nil
	}
	return &GlobMatcher{patterns: patterns}, nil
}

func (m *GlobMatcher) MatchesNamespacedName(namespace, name string) bool {
	for _, p := range m.patterns {
		nsMatch, _ := path.Match(p.nsPattern, namespace)
		saMatch, _ := path.Match(p.saPattern, name)
		if nsMatch && saMatch {
			return true
		}
	}
	return false
}

// validateGlobPattern checks whether the pattern is a valid glob pattern by
// attempting a match with path.Match. An empty pattern is valid (matches only
// empty strings).
func validateGlobPattern(pattern string) error {
	_, err := path.Match(pattern, "")
	if err != nil {
		return fmt.Errorf("invalid glob syntax: %w", err)
	}
	return nil
}
