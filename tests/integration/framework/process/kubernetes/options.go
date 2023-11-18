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
	"path"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
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

func WithClusterDaprConfigurationList(t *testing.T, configs *configapi.ConfigurationList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v1alpha1/configurations", configs)
}

func WithClusterDaprComponentList(t *testing.T, comps *compapi.ComponentList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v1alpha1/components", comps)
}

func WithClusterDaprHTTPEndpointList(t *testing.T, endpoints *httpendapi.HTTPEndpointList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v1alpha1/httpendpoints", endpoints)
}

func WithClusterPodList(t *testing.T, pods *corev1.PodList) Option {
	return handleClusterListResource(t, "/api/v1/pods", pods)
}

func WithClusterServiceList(t *testing.T, services *corev1.ServiceList) Option {
	return handleClusterListResource(t, "/api/v1/services", services)
}

func WithClusterDeploymentList(t *testing.T, deploys *appsv1.DeploymentList) Option {
	return handleClusterListResource(t, "/apis/apps/v1/deployments", deploys)
}

func WithClusterStatefulSetList(t *testing.T, ss *appsv1.StatefulSetList) Option {
	return handleClusterListResource(t, "/apis/apps/v1/statefulsets", ss)
}

func WithDaprConfigurationGet(t *testing.T, ns, name string, config *configapi.Configuration) Option {
	return handleGetResource(t, "/apis/dapr.io/v1alpha1", "configurations", ns, name, config)
}

func WithSecretGet(t *testing.T, ns, name string, secret *corev1.Secret) Option {
	return handleGetResource(t, "/api/v1", "secrets", ns, name, secret)
}

func WithConfigMapGet(t *testing.T, ns, name string, configmap *corev1.ConfigMap) Option {
	return handleGetResource(t, "/api/v1", "configmaps", ns, name, configmap)
}

func handleClusterListResource(t *testing.T, path string, obj any) Option {
	return func(o *options) {
		obj, err := json.Marshal(obj)
		require.NoError(t, err)
		o.handlers = append(o.handlers, handleRoute{
			path: path,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Length", strconv.Itoa(len(obj)))
				w.Header().Add("Content-Type", "application/json")
				w.Write(obj)
			},
		})
	}
}

func handleGetResource(t *testing.T, apigv, resource, ns, name string, obj any) Option {
	return func(o *options) {
		obj, err := json.Marshal(obj)
		require.NoError(t, err)
		o.handlers = append(o.handlers, handleRoute{
			path: path.Join(apigv, "namespaces", ns, resource, name),
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Content-Length", strconv.Itoa(len(obj)))
				w.Header().Add("Content-Type", "application/json")
				w.Write(obj)
			},
		})
	}
}
