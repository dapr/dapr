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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	mcpapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	resapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subv1api "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subv2api "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorconsts "github.com/dapr/dapr/pkg/injector/consts"
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

func WithClusterDaprSubscriptionList(t *testing.T, subs *subv1api.SubscriptionList) Option {
	subv2List := new(subv2api.SubscriptionList)
	for _, s := range subs.Items {
		subv2 := new(subv2api.Subscription)
		require.NoError(t, subv2.ConvertFrom(s.DeepCopy()))
		subv2List.Items = append(subv2List.Items, *subv2)
	}
	return handleClusterListResource(t, "/apis/dapr.io/v2alpha1/subscriptions", subv2List)
}

func WithClusterDaprSubscriptionListV2(t *testing.T, subs *subv2api.SubscriptionList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v2alpha1/subscriptions", subs)
}

func WithClusterDaprComponentList(t *testing.T, comps *compapi.ComponentList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v1alpha1/components", comps)
}

func WithClusterDaprComponentListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/apis/dapr.io/v1alpha1/components", store)
}

func WithClusterDaprSubscriptionListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/apis/dapr.io/v2alpha1/subscriptions", store)
}

func WithClusterDaprSubscriptionV2ListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/apis/dapr.io/v2alpha1/subscriptions", store)
}

func WithClusterNamespaceListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/api/v1/namespaces", store)
}

func WithClusterDaprWorkflowAccessPolicyListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/apis/dapr.io/v1alpha1/workflowaccesspolicies", store)
}

func WithClusterDaprHTTPEndpointList(t *testing.T, endpoints *httpendapi.HTTPEndpointList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v1alpha1/httpendpoints", endpoints)
}

func WithClusterDaprMCPServerList(t *testing.T, mcpservers *mcpapi.MCPServerList) Option {
	return handleClusterListResource(t, "/apis/dapr.io/v1alpha1/mcpservers", mcpservers)
}

func WithClusterDaprConfigurationListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/apis/dapr.io/v1alpha1/configurations", store)
}

func WithClusterDaprHTTPEndpointListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/apis/dapr.io/v1alpha1/httpendpoints", store)
}

func WithClusterDaprResiliencyListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/apis/dapr.io/v1alpha1/resiliencies", store)
}

func WithClusterPodList(t *testing.T, pods *corev1.PodList) Option {
	return handleClusterListResource(t, "/api/v1/pods", pods)
}

func WithClusterPodListSelector(t *testing.T, selector string, pods *corev1.PodList) Option {
	return func(o *options) {
		handle := handleObj(t, pods)
		o.handlers["/api/v1/pods"] = func(w http.ResponseWriter, r *http.Request) {
			// The fake informer handles WATCH requests before this callback.
			// Controller-runtime applies the same selector to LIST and WATCH.
			assert.Equal(t, selector, r.URL.Query().Get("labelSelector"))
			handle(w, r)
		}
	}
}

func WithNamespacedPodList(t *testing.T, ns string, pods *corev1.PodList) Option {
	return handleClusterListResource(t, "/api/v1/namespaces/"+ns+"/pods", pods)
}

// WithNamespacedPodListSelector serves pods on the namespaced pod list path
// and asserts every request uses the given label selector.
func WithNamespacedPodListSelector(t *testing.T, ns, selector string, pods *corev1.PodList) Option {
	return func(o *options) {
		handle := handleObj(t, pods)
		o.handlers["/api/v1/namespaces/"+ns+"/pods"] = func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, selector, r.URL.Query().Get("labelSelector"))
			handle(w, r)
		}
	}
}

func WithNamespacedPodListFromStore(t *testing.T, ns string, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/api/v1/namespaces/"+ns+"/pods", store)
}

// DaprPod returns a pod as the injector leaves it for appID, assigned the
// given dapr.io/config annotation value.
func DaprPod(ns, name, appID, config string) corev1.Pod {
	return corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{injectorconsts.SidecarAppIDLabel: appID},
			Annotations: map[string]string{
				annotations.KeyEnabled: "true",
				annotations.KeyAppID:   appID,
				annotations.KeyConfig:  config,
			},
		},
	}
}

// WithDaprPodList serves the pods on both the cluster and namespaced pod list
// paths, so they are seen by the operator's pod cache and its direct API reads.
// Must be passed after WithBaseOperatorAPI, which serves empty pod lists.
func WithDaprPodList(t *testing.T, ns string, pods ...corev1.Pod) Option {
	list := &corev1.PodList{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
		Items:    pods,
	}
	return func(o *options) {
		WithClusterPodList(t, list)(o)
		WithNamespacedPodList(t, ns, list)(o)
	}
}

// WithDaprPods serves one DaprPod per app ID, named after the app ID and
// assigned the mapped configuration, so the operator authorizes each app's
// GetConfiguration.
func WithDaprPods(t *testing.T, ns string, appConfigs map[string]string) Option {
	pods := make([]corev1.Pod, 0, len(appConfigs))
	for appID, config := range appConfigs {
		pods = append(pods, DaprPod(ns, appID, appID, config))
	}
	return WithDaprPodList(t, ns, pods...)
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

func WithClusterServiceAccountList(t *testing.T, services *corev1.ServiceAccountList) Option {
	return handleClusterListResource(t, "/api/v1/serviceaccounts", services)
}

func WithClusterNamespaceList(t *testing.T, namespaces *corev1.NamespaceList) Option {
	return handleClusterListResource(t, "/api/v1/namespaces", namespaces)
}

func WithDaprConfigurationGet(t *testing.T, config *configapi.Configuration) Option {
	return handleGetResource(t, "/apis/dapr.io/v1alpha1", "configurations", config.Namespace, config.Name, config)
}

func WithSecretGet(t *testing.T, secret *corev1.Secret) Option {
	return handleGetResource(t, "/api/v1", "secrets", secret.Namespace, secret.Name, secret)
}

func WithPodGet(t *testing.T, pod *corev1.Pod) Option {
	return handleGetResource(t, "/api/v1", "pods", pod.Namespace, pod.Name, pod)
}

func WithStatefulSetGet(t *testing.T, statefulset *appsv1.StatefulSet) Option {
	return handleGetResource(t, "/apis/apps/v1", "statefulsets", statefulset.Namespace, statefulset.Name, statefulset)
}

func WithSecretList(t *testing.T, secrets *corev1.SecretList) Option {
	return handleClusterListResource(t, "/api/v1/secrets", secrets)
}

func WithClusterSecretListFromStore(t *testing.T, store *store.Store) Option {
	return handleClusterListResourceFromStore(t, "/api/v1/secrets", store)
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
			WithClusterPodList(t, &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}}),
			WithNamespacedPodList(t, ns, &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}}),
			WithClusterServiceList(t, &corev1.ServiceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceList"}}),
			WithClusterStatefulSetList(t, &appsv1.StatefulSetList{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "StatefulSetList"}}),
			WithClusterDeploymentList(t, &appsv1.DeploymentList{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "DeploymentList"}}),
			WithClusterServiceAccountList(t, &corev1.ServiceAccountList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccountList"}}),
			WithClusterDaprComponentList(t, &compapi.ComponentList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ComponentList"}}),
			WithClusterDaprSubscriptionListV2(t, &subv2api.SubscriptionList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v2alpha1", Kind: "SubscriptionList"}}),
			WithClusterDaprHTTPEndpointList(t, &httpendapi.HTTPEndpointList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "HTTPEndpointList"}}),
			WithClusterDaprConfigurationList(t, &configapi.ConfigurationList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ConfigurationList"}}),
			WithClusterDaprResiliencyList(t, &resapi.ResiliencyList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "ResiliencyList"}}),
			WithClusterDaprMCPServerList(t, &mcpapi.MCPServerList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "MCPServerList"}}),
			handleClusterListResource(t, "/apis/dapr.io/v1alpha1/workflowaccesspolicies", &wfaclapi.WorkflowAccessPolicyList{TypeMeta: metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicyList"}}),
			WithClusterNamespaceList(t, &corev1.NamespaceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "NamespaceList"}}),
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
		assert.NoError(t, err)
		w.Header().Add("Content-Length", strconv.Itoa(len(objB)))
		w.Header().Add("Content-Type", "application/json")
		w.Write(objB)
	}
}

func handleObjFromStore(t *testing.T, store *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		objB, err := json.Marshal(store.Objects())
		assert.NoError(t, err)
		w.Header().Add("Content-Length", strconv.Itoa(len(objB)))
		w.Header().Add("Content-Type", "application/json")
		w.Write(objB)
	}
}
