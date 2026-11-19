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
	"fmt"
	"net/http"
	"slices"
	"strings"
)

type pathMatching struct {
	mux           *http.ServeMux
	returnRawPath bool
}

// newPathMatching creates a new pathMatching instance using ServeMux.
// The root path ("/") must always be registered:
// - If the user explicitly registers the root path, we match it accordingly.
// - If the root path is not explicitly registered:
//   - If legacy is true, we fall back to using an empty handler function.
//   - If legacy is false, we match the root path to an empty string.
//
// All other paths in the 'paths' slice are cleaned, sorted, and registered.
// A path that cannot be registered is logged and ignored.
func newPathMatching(paths []string, legacy bool) *pathMatching {
	if len(paths) == 0 {
		return nil
	}

	mux := http.NewServeMux()
	pm := &pathMatching{mux: mux}

	cleanPaths, foundRootPath := cleanAndSortPaths(paths)

	for _, pattern := range cleanPaths {
		if err := register(mux, pattern); err != nil {
			log.Errorf("Ignoring an invalid entry in 'spec.metrics.http.pathMatching': %v", err)
		}
	}

	// Handle Legacy Root Fallback
	if !foundRootPath {
		if legacy {
			if err := register(mux, "/"); err != nil {
				log.Errorf("Ignoring the legacy root path for 'spec.metrics.http.pathMatching': %v", err)
			}
			pm.returnRawPath = true
		}
	}

	return pm
}

// register adds the pattern to the mux.
// http.ServeMux panics on a pattern it cannot parse and on a pattern that
// overlaps one registered before, so the panic is turned into an error.
func register(mux *http.ServeMux, pattern string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pattern %q: %v", pattern, r)
		}
	}()

	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {})
	return nil
}

// cleanAndSortPaths takes user input paths and returns a sorted, deduplicated list
// of all patterns to be registered, including the auto-generated invoke variants.
func cleanAndSortPaths(paths []string) ([]string, bool) {
	cleanPaths := make([]string, 0, len(paths)*2)
	foundRootPath := false

	for _, raw := range paths {
		p := NormalizeHTTPPath(raw)
		cleanPaths = append(cleanPaths, p)

		if p == "/" {
			foundRootPath = true
		}

		// Auto-register Service Invocation prefix
		if !strings.HasPrefix(p, "/v1.0/invoke/") {
			invokePath := "/v1.0/invoke/{app_id}/method" + p
			cleanPaths = append(cleanPaths, invokePath)
		}
	}

	slices.Sort(cleanPaths)
	return slices.Compact(cleanPaths), foundRootPath
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
	if req.URL != nil {
		req.URL.Path = cleanPath
	}

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
