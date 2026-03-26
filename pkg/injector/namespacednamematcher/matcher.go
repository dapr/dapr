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

// ServiceAccountMatcher matches a namespace and name pair against a set of
// configured patterns.
type ServiceAccountMatcher interface {
	MatchesNamespacedName(namespace, name string) bool
}

// CompositeMatcher combines multiple ServiceAccountMatcher implementations,
// returning true if any of the inner matchers matches.
type CompositeMatcher struct {
	matchers []ServiceAccountMatcher
}

// NewCompositeMatcher creates a CompositeMatcher from the given matchers.
// Nil matchers are silently skipped. Returns nil if no non-nil matchers are
// provided.
func NewCompositeMatcher(matchers ...ServiceAccountMatcher) *CompositeMatcher {
	filtered := make([]ServiceAccountMatcher, 0, len(matchers))
	for _, m := range matchers {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &CompositeMatcher{matchers: filtered}
}

func (c *CompositeMatcher) MatchesNamespacedName(namespace, name string) bool {
	for _, m := range c.matchers {
		if m.MatchesNamespacedName(namespace, name) {
			return true
		}
	}
	return false
}
