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

package evict

import (
	"net/http"
	"strings"
	"sync"
)

// Filter treats a deactivation with no callback since the last one as an eviction.
type Filter struct {
	held sync.Map
}

func New() *Filter {
	return new(Filter)
}

func (f *Filter) Called(actorType, actorID string) {
	f.held.Store(actorType+"/"+actorID, struct{}{})
}

func (f *Filter) IsEviction(actorType, actorID string) bool {
	_, ok := f.held.LoadAndDelete(actorType + "/" + actorID)
	return !ok
}

// Handler drops eviction DELETEs on /actors/{type}/{id} and passes everything else to next.
func (f *Filter) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, ok := strings.CutPrefix(r.URL.Path, "/actors/")
		parts := strings.SplitN(path, "/", 3)
		if !ok || len(parts) < 2 {
			next.ServeHTTP(w, r)
			return
		}

		if r.Method != http.MethodDelete {
			f.Called(parts[0], parts[1])
		} else if f.IsEviction(parts[0], parts[1]) {
			return
		}

		next.ServeHTTP(w, r)
	})
}
