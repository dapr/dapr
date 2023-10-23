/*
Copyright 2023 The Dapr Authors
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
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
)

type handleRoute struct {
	path    string
	handler http.HandlerFunc
}

// options contains the options for running a mock Kubernetes API server.
type options struct {
	handlers []handleRoute
}

func WithPath(path string, handler http.HandlerFunc) Option {
	return func(o *options) {
		o.handlers = append(o.handlers, handleRoute{
			path:    path,
			handler: handler,
		})
	}
}

func WithDaprConfigurationList(t *testing.T, configs *configapi.ConfigurationList) Option {
	return func(o *options) {
		obj, err := json.Marshal(configs)
		require.NoError(t, err)
		o.handlers = append(o.handlers, handleRoute{
			path: "/apis/dapr.io/v1alpha1/configurations",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Type", "application/json")
				w.Write(obj)
			},
		})
	}
}

func WithSecretGet(t *testing.T, ns, name string, secret *corev1.Secret) Option {
	return func(o *options) {
		obj, err := json.Marshal(secret)
		require.NoError(t, err)
		o.handlers = append(o.handlers, handleRoute{
			path: "/api/v1/namespaces/" + ns + "/secrets/" + name,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Type", "application/json")
				w.Write(obj)
			},
		})
	}
}

func WithConfigMapGet(t *testing.T, ns, name string, configmap *corev1.ConfigMap) Option {
	return func(o *options) {
		obj, err := json.Marshal(configmap)
		require.NoError(t, err)
		o.handlers = append(o.handlers, handleRoute{
			path: "/api/v1/namespaces/" + ns + "/configmaps/" + name,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Type", "application/json")
				w.Write(obj)
			},
		})
	}
}

func WithPodList(t *testing.T, pods *corev1.PodList) Option {
	return func(o *options) {
		obj, err := json.Marshal(pods)
		require.NoError(t, err)
		o.handlers = append(o.handlers, handleRoute{
			path: "/api/v1/pods",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Type", "application/json")
				w.Write(obj)
			},
		})
	}
}
