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

func newPathMatching(paths []string, legacy bool) *pathMatching {
	if paths == nil {
		return nil
	}

	if len(paths) == 0 {
		return nil
	}

	cleanPaths := cleanAndSortPaths(paths)

	mux := http.NewServeMux()

	// Skip the root path if legacy mode is enabled.
	if legacy {
		mux.Handle("/", emptyHandlerFunc)
	} else {
		mux.Handle("/", pathMatchHandlerFunc("/"))
	}

	for _, pattern := range cleanPaths {
		mux.Handle(pattern, pathMatchHandlerFunc(pattern))
	}

	return &pathMatching{
		mux: mux,
	}
}

// cleanAndSortPaths ensures that we don't have duplicates and removes root path
func cleanAndSortPaths(paths []string) []string {
	slices.Sort(paths)
	paths = slices.Compact(paths)
	cleanPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "/" {
			continue
		}
		cleanPaths = append(cleanPaths, path)
	}
	return cleanPaths
}

func (pm *pathMatching) enabled() bool {
	return pm != nil && pm.mux != nil
}

func (pm *pathMatching) matchPath(path string) (string, bool) {
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
	http.ResponseWriter
	matchedPath string
}
