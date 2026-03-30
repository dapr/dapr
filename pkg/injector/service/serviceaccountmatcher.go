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

package service

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// NewServiceAccountMatcher takes "namespace:name" glob patterns and returns a
// function that reports whether a namespace:name pair matches any of them.
// Both components support glob syntax (*, ?, [...]). Empty strings are
// silently skipped. Returns nil when no valid patterns are provided.
func NewServiceAccountMatcher(patterns ...string) (func(namespace, name string) bool, error) {
	var matchers []func(namespace, name string) bool

	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}

		if strings.Count(pattern, ":") != 1 {
			return nil, fmt.Errorf("invalid pattern %q, must contain exactly one ':' separating namespace and name", pattern)
		}

		parts := strings.Split(pattern, ":")
		nsPattern := strings.TrimSpace(parts[0])
		saPattern := strings.TrimSpace(parts[1])

		if nsPattern == "" && saPattern == "" {
			return nil, errors.New("namespace and name patterns cannot both be empty")
		}
		if _, err := path.Match(nsPattern, ""); err != nil {
			return nil, fmt.Errorf("invalid namespace pattern %q: %w", nsPattern, err)
		}
		if _, err := path.Match(saPattern, ""); err != nil {
			return nil, fmt.Errorf("invalid service account pattern %q: %w", saPattern, err)
		}

		matchers = append(matchers, func(namespace, name string) bool {
			nsMatch, _ := path.Match(nsPattern, namespace)
			saMatch, _ := path.Match(saPattern, name)
			return nsMatch && saMatch
		})
	}

	if len(matchers) == 0 {
		return nil, nil
	}

	return func(namespace, name string) bool {
		for _, m := range matchers {
			if m(namespace, name) {
				return true
			}
		}
		return false
	}, nil
}
