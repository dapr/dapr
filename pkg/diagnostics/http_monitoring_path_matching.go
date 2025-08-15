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
	"net/url"
	"slices"
	"strings"
)

var (
	pathMatchHandlerFunc = func(pattern string) http.HandlerFunc {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw, ok := w.(*pathMatchingRW)
			if !ok {
				log.Errorf("Failed to cast to PathMatchingRW")
				return
			}
			rw.matchedPath = pattern
		})
	}

	emptyHandlerFunc = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
)

type pathMatching struct {
	mux *http.ServeMux
}

// newPathMatching creates a new pathMatching instance.
// The root path ("/") must always be registered:
// - If the user explicitly registers the root path, we match it accordingly.
// - If the root path is not explicitly registered:
//   - If legacy is true, we fall back to using an empty handler function.
//   - If legacy is false, we match the root path to an empty string.
//
// All other paths in the 'paths' slice are cleaned, sorted, and registered.
func newPathMatching(paths []string, legacy bool) *pathMatching {
	if paths == nil {
		return nil
	}

	if len(paths) == 0 {
		return nil
	}

	mux := http.NewServeMux()

	cleanPaths, foundRootPath := cleanAndSortPaths(paths)

	if !foundRootPath {
		if legacy {
			mux.Handle("/", emptyHandlerFunc)
		} else {
			mux.Handle("/", pathMatchHandlerFunc(""))
		}
	}

	for _, pattern := range cleanPaths {
		mux.Handle(pattern, pathMatchHandlerFunc(pattern))
	}

	return &pathMatching{
		mux: mux,
	}
}

// cleanAndSortPaths processes the given slice of paths by sorting and compacting it,
// and checks if the root path ("/") is included.
func cleanAndSortPaths(paths []string) ([]string, bool) {
	foundRootPath := false
	slices.Sort(paths)
	paths = slices.Compact(paths)
	cleanPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "/" {
			foundRootPath = true
		}
		cleanPaths = append(cleanPaths, path)
	}
	return cleanPaths, foundRootPath
}

func (pm *pathMatching) enabled() bool {
	return pm != nil && pm.mux != nil
}

func (pm *pathMatching) match(path string) (string, bool) {
	if !pm.enabled() {
		return "", false
	}

	if path == "" {
		return "", false
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	req := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Path: path,
		},
	}

	crw := &pathMatchingRW{matchedPath: path}
	pm.mux.ServeHTTP(crw, req)

	return crw.matchedPath, true
}

type pathMatchingRW struct {
	matchedPath string
	header      http.Header
}

// Header returns a non-nil header map
func (rw *pathMatchingRW) Header() http.Header {
	if rw.header == nil {
		rw.header = make(http.Header)
	}
	return rw.header
}

func (rw *pathMatchingRW) Write(b []byte) (int, error) {
	return len(b), nil
}

func (rw *pathMatchingRW) WriteHeader(statusCode int) {
	// no-op
}
