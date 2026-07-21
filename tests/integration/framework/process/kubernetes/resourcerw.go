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

package kubernetes

import (
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

// ResourceRW serves a single named resource from the mock Kubernetes API
// server with both read (GET) and write (PUT) support. Writes replace the
// stored object and are visible to subsequent reads, so processes under test
// get read-your-writes semantics. Tests can inspect the latest written object
// via Current.
type ResourceRW[T any] struct {
	path string
	lock sync.RWMutex
	objB []byte
}

// NewSecretRW returns a read-write handler for the given Secret.
func NewSecretRW(t *testing.T, secret *corev1.Secret) *ResourceRW[corev1.Secret] {
	t.Helper()
	return newResourceRW[corev1.Secret](t, path.Join("/api/v1", "namespaces", secret.Namespace, "secrets", secret.Name), secret)
}

// NewConfigMapRW returns a read-write handler for the given ConfigMap.
func NewConfigMapRW(t *testing.T, configmap *corev1.ConfigMap) *ResourceRW[corev1.ConfigMap] {
	t.Helper()
	return newResourceRW[corev1.ConfigMap](t, path.Join("/api/v1", "namespaces", configmap.Namespace, "configmaps", configmap.Name), configmap)
}

func newResourceRW[T any](t *testing.T, path string, obj any) *ResourceRW[T] {
	t.Helper()
	objB, err := json.Marshal(obj)
	require.NoError(t, err)
	return &ResourceRW[T]{path: path, objB: objB}
}

// Option registers the read-write handler with the mock Kubernetes API
// server.
func (r *ResourceRW[T]) Option() Option {
	return WithPath(r.path, r.handle)
}

// Current returns the latest stored object.
func (r *ResourceRW[T]) Current(t *testing.T) *T {
	t.Helper()
	r.lock.RLock()
	defer r.lock.RUnlock()
	obj := new(T)
	require.NoError(t, json.Unmarshal(r.objB, obj))
	return obj
}

// Set replaces the stored object, e.g. to seed state before the process under
// test starts.
func (r *ResourceRW[T]) Set(t *testing.T, obj *T) {
	t.Helper()
	objB, err := json.Marshal(obj)
	require.NoError(t, err)
	r.lock.Lock()
	defer r.lock.Unlock()
	r.objB = objB
}

func (r *ResourceRW[T]) handle(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodPut {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		r.lock.Lock()
		r.objB = body
		r.lock.Unlock()
	}

	r.lock.RLock()
	objB := r.objB
	r.lock.RUnlock()
	w.Header().Add("Content-Length", strconv.Itoa(len(objB)))
	w.Header().Add("Content-Type", "application/json")
	w.Write(objB)
}
