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

package informer

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
)

// Informer is a fake informer that adds events to the Kubernetes API server to
// send events to clients.
type Informer struct {
	lock   sync.Mutex
	active map[string][][]byte
}

func New() *Informer {
	return &Informer{
		active: make(map[string][][]byte),
	}
}

func (i *Informer) Handler(t *testing.T, wrapped http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.Query().Has("watch") || r.URL.Query().Get("watch") != "true" {
			wrapped.ServeHTTP(w, r)
			return
		}

		i.lock.Lock()
		path := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/apis/"), "/api/")

		var gvk schema.GroupVersionKind
		split := strings.Split(path, "/")
		require.GreaterOrEqual(t, len(split), 2, "invalid path: %s", path)
		if split[0] == "v1" {
			gvk = schema.GroupVersionKind{Group: "", Version: "v1"}
			split = split[1:]
		} else {
			gvk = schema.GroupVersionKind{Group: strings.Split(path, "/")[0], Version: strings.Split(path, "/")[1]}
			split = split[2:]
		}
		if split[0] == "namespaces" {
			split = split[2:]
		}
		gvk.Kind = split[0]

		w.Header().Add("Transfer-Encoding", "chunked")
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if len(i.active[gvk.String()]) > 0 {
			w.Write(i.active[gvk.String()][0])
			i.active[gvk.String()] = i.active[gvk.String()][1:]
		}
		w.(http.Flusher).Flush()
		i.lock.Unlock()
	}
}

func (i *Informer) Add(t *testing.T, obj runtime.Object) {
	t.Helper()
	i.inform(t, obj, string(watch.Added))
}

func (i *Informer) Modify(t *testing.T, obj runtime.Object) {
	t.Helper()
	i.inform(t, obj, string(watch.Modified))
}

func (i *Informer) Delete(t *testing.T, obj runtime.Object) {
	t.Helper()
	i.inform(t, obj, string(watch.Deleted))
}

func (i *Informer) inform(t *testing.T, obj runtime.Object, event string) {
	t.Helper()
	i.lock.Lock()
	defer i.lock.Unlock()

	gvk := i.objToGVK(t, obj)

	watchObjB, err := json.Marshal(obj)
	require.NoError(t, err)

	watchEvent, err := json.Marshal(&metav1.WatchEvent{
		Type:   event,
		Object: runtime.RawExtension{Raw: watchObjB, Object: obj},
	})
	require.NoError(t, err)
	i.active[gvk.String()] = append(i.active[gvk.String()], watchEvent)
}

func (i *Informer) objToGVK(t *testing.T, obj runtime.Object) schema.GroupVersionKind {
	t.Helper()

	switch obj.(type) {
	case *appsv1.Deployment:
		return schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "deployments"}
	case *compapi.Component:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "components"}
	case *configapi.Configuration:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "configurations"}
	case *httpendapi.HTTPEndpoint:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "httpendpoints"}
	case *resiliencyapi.Resiliency:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "resiliencies"}
	case *corev1.Pod:
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "pods"}
	case *corev1.Service:
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "services"}
	case *corev1.Secret:
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "secrets"}
	case *corev1.ConfigMap:
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "configmaps"}
	default:
		require.Fail(t, "unknown type: %T", obj)
		return schema.GroupVersionKind{}
	}
}
