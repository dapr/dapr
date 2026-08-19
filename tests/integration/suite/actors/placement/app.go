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

package placement

import (
	"net/http"
	"testing"

	chi "github.com/go-chi/chi/v5"

	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
)

func newActorApp(t *testing.T) *prochttp.HTTP {
	t.Helper()

	handler := chi.NewRouter()
	handler.Get("/dapr/config", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.Delete("/actors/{actorType}/{actorId}", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.Put("/actors/{actorType}/{actorId}/method/foo", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`bar`))
	})

	return prochttp.New(t, prochttp.WithHandler(handler))
}

const actorStateStore = `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: actorStateStore
    value: true
`
