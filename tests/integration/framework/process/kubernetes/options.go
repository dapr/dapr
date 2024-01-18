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

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	resapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework/process/kubernetes/store"
)

// options contains the options for running a mock Kubernetes API server.
type options struct {
	handlers map[string]http.HandlerFunc
}

func WithPath(path string, handler http.HandlerFunc) Option {
	return func(o *options) {
		o.handlers[path] = handler
	}
}

func WithClusterDaprConfigurationList(t *testing.T, configs *configapi.ConfigurationList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v1alpha1/configurations", configs)
}

func WithClusterDaprResiliencyList(t *testing.T, res *resapi.ResiliencyList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v1alpha1/resiliencies", res)
}

func WithClusterDaprSubscriptionList(t *testing.T, subs *subapi.SubscriptionList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v2alpha1/subscriptions", subs)
}

func WithClusterDaprComponentList(t *testing.T, comps *compapi.ComponentList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v1alpha1/components", comps)
}

func WithClusterDaprComponentListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/apis/dapr.io/v1alpha1/components", store)
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

func WithDaprConfigurationGet(t *testing.T, config *configapi.Configuration) Option {
	return handleGetResource(t, "/apis/dapr.io/v1alpha1", "configurations", config.Namespace, config.Name, config)
}

func WithSecretGet(t *testing.T, secret *corev1.Secret) Option {
	return handleGetResource(t, "/api/v1", "secrets", secret.Namespace, secret.Name, secret)
}

func WithDaprResiliencyGet(t *testing.T, ns, name string, res *resapi.Resiliency) Option {
	return handleGetResource(t, "/apis/dapr.io/v1alpha1", "resiliencies", ns, name, res)
}

func WithConfigMapGet(t *testing.T, configmap *corev1.ConfigMap) Option {
	return handleGetResource(t, "/api/v1", "configmaps", configmap.Namespace, configmap.Name, configmap)
}

func WithBaseOperatorAPI(t *testing.T, td spiffeid.TrustDomain, ns string, sentryPort int) Option {
	return func(o *options) {
		for _, op := range []Option{
			WithDaprConfigurationGet(t, &configapi.Configuration{
				TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
				ObjectMeta: metav1.ObjectMeta{Name: "daprsystem", Namespace: ns},
				Spec: configapi.ConfigurationSpec{
					MTLSSpec: &configapi.MTLSSpec{
						ControlPlaneTrustDomain: td.String(),
						SentryAddress:           "localhost:" + strconv.Itoa(sentryPort),
					},
				},
			}),
			WithClusterServiceList(t, &corev1.ServiceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceList"}}),
			WithClusterStatefulSetList(t, &appsv1.StatefulSetList{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSetList"}}),
			WithClusterDeploymentList(t, &appsv1.DeploymentList{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DeploymentList"}}),
			WithClusterDaprComponentList(t, &compapi.ComponentList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ComponentList"}}),
			WithClusterDaprHTTPEndpointList(t, &httpendapi.HTTPEndpointList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "HTTPEndpointList"}}),
			WithClusterDaprResiliencyList(t, &resapi.ResiliencyList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ResiliencyList"}}),
		} {
			op(o)
		}
	}
}

func handleClusterListResource(t *testing.T, path string, obj runtime.Object) Option {
	return func(o *options) {
		o.handlers[path] = handleObj(t, obj)
	}
}

func handleClusterListResourceFromStore(t *testing.T, path string, store *store.Store) Option {
	return func(o *options) {
		o.handlers[path] = handleObjFromStore(t, store)
	}
}

func handleGetResource(t *testing.T, apigv, resource, ns, name string, obj runtime.Object) Option {
	return func(o *options) {
		o.handlers[path.Join(apigv, "namespaces", ns, resource, name)] = handleObj(t, obj)
	}
}

// func handleObj(t *testing.T, gvk metav1.GroupVersionKind, obj runtime.Object) http.HandlerFunc {
func handleObj(t *testing.T, obj runtime.Object) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		objB, err := json.Marshal(obj)
		require.NoError(t, err)
		w.Header().Add("Content-Length", strconv.Itoa(len(objB)))
		w.Header().Add("Content-Type", "application/json")
		w.Write(objB)
	}
}

func handleObjFromStore(t *testing.T, store *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		objB, err := json.Marshal(store.Objects())
		require.NoError(t, err)
		w.Header().Add("Content-Length", strconv.Itoa(len(objB)))
		w.Header().Add("Content-Type", "application/json")
		w.Write(objB)
	}
}
