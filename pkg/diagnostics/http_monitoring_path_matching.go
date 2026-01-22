/*
Copyright 2024 The Dapr Authors
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

package diagnostics

import (
	"net/http"
	"slices"
	"strings"
)

type pathMatching struct {
	mux           *http.ServeMux
	returnRawPath bool
}

// newPathMatching creates a new pathMatching instance using ServeMux
// Paths are normalized (double slashes merged, leading slash ensured) and registered.
// Behavior for unmatched paths depends on legacy mode:
// - Legacy (increasedCardinality=true): Register "/" fallback and return raw path to preserve cardinality
// - Strict (increasedCardinality=false): Do NOT register "/" so unmatched paths return empty string (low cardinality)
func newPathMatching(paths []string, legacy bool) *pathMatching {
	if len(paths) == 0 {
		return nil
	}

	mux := http.NewServeMux()
	pm := &pathMatching{mux: mux}

	slices.Sort(paths)
	paths = slices.Compact(paths)

	hasRoot := false
	for _, p := range paths {
		pattern := NormalizeHTTPPath(p)
		if pattern == "/" {
			hasRoot = true
		}
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {})
	}

	// Handle behavior for unmatched paths
	if !hasRoot {
		if legacy {
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
			pm.returnRawPath = true
		}
	}

	return pm
}

func (pm *pathMatching) enabled() bool {
	return pm != nil && pm.mux != nil
}

func (pm *pathMatching) match(path string) (string, bool) {
	if !pm.enabled() || path == "" {
		return "", false
	}

	cleanPath := NormalizeHTTPPath(path)
	req, _ := http.NewRequest(http.MethodGet, cleanPath, nil)

	_, pattern := pm.mux.Handler(req)

	if pattern == "/" && pm.returnRawPath {
		return cleanPath, true
	}

	if pattern == "" {
		return "", true
	}

	return pattern, true
}

// NormalizeHTTPPath merges double slashes and ensures leading slash.
func NormalizeHTTPPath(p string) string {
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
