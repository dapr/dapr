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
	"net/http"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// TrustBundleStore is an in-memory, stateful Secret and ConfigMap pair served
// with GET and PUT support, so tests can exercise sentry writing its trust
// bundle in Kubernetes mode.
type TrustBundleStore struct {
	lock      sync.RWMutex
	secret    *corev1.Secret
	configMap *corev1.ConfigMap
}

func NewTrustBundleStore(secret *corev1.Secret, configMap *corev1.ConfigMap) *TrustBundleStore {
	return &TrustBundleStore{
		secret:    secret,
		configMap: configMap,
	}
}

func (s *TrustBundleStore) Secret() *corev1.Secret {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.secret.DeepCopy()
}

func (s *TrustBundleStore) ConfigMap() *corev1.ConfigMap {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.configMap.DeepCopy()
}

func (s *TrustBundleStore) SetSecret(secret *corev1.Secret) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.secret = secret.DeepCopy()
}

func (s *TrustBundleStore) SetConfigMap(configMap *corev1.ConfigMap) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.configMap = configMap.DeepCopy()
}

// WithTrustBundleStore serves the store's Secret and ConfigMap with GET and
// PUT support at their namespaced paths.
func WithTrustBundleStore(t *testing.T, store *TrustBundleStore) Option {
	t.Helper()

	writeObj := func(w http.ResponseWriter, obj any) {
		objB, err := json.Marshal(obj)
		if err != nil {
			t.Errorf("failed to marshal object: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Add("Content-Type", "application/json")
		w.Write(objB)
	}

	secret := store.Secret()
	configMap := store.ConfigMap()
	secretPath := "/api/v1/namespaces/" + secret.Namespace + "/secrets/" + secret.Name
	configMapPath := "/api/v1/namespaces/" + configMap.Namespace + "/configmaps/" + configMap.Name

	return func(o *options) {
		o.handlers["GET "+secretPath] = func(w http.ResponseWriter, r *http.Request) {
			writeObj(w, store.Secret())
		}
		o.handlers["PUT "+secretPath] = func(w http.ResponseWriter, r *http.Request) {
			var sec corev1.Secret
			if err := json.NewDecoder(r.Body).Decode(&sec); err != nil {
				t.Errorf("failed to decode secret: %s", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			store.SetSecret(&sec)
			writeObj(w, store.Secret())
		}
		o.handlers["GET "+configMapPath] = func(w http.ResponseWriter, r *http.Request) {
			writeObj(w, store.ConfigMap())
		}
		o.handlers["PUT "+configMapPath] = func(w http.ResponseWriter, r *http.Request) {
			var cm corev1.ConfigMap
			if err := json.NewDecoder(r.Body).Decode(&cm); err != nil {
				t.Errorf("failed to decode configmap: %s", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			store.SetConfigMap(&cm)
			writeObj(w, store.ConfigMap())
		}
	}
}
