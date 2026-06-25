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

package cache

import (
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatormeta "github.com/dapr/dapr/pkg/operator/meta"
)

// PodMetadataCacheOptions are the options for NewPodMetadataCache.
type PodMetadataCacheOptions struct {
	RestConfig *rest.Config
	Namespace  string
	SyncPeriod *time.Duration
	// HTTPClient and Mapper are reused from the operator manager to avoid a second
	// round of API discovery (an expensive REST mapper build) per operator.
	HTTPClient *http.Client
	Mapper     meta.RESTMapper
}

// daprAnnotationPrefix is the prefix of the annotations consumed off cached pods.
const daprAnnotationPrefix = "dapr.io/"

// podMetaDevNull is the sinkhole for non-Dapr pods, coalescing them into a single
// cached entry (they all share this name/namespace key).
var podMetaDevNull = newPodMetadata(randomName, randomName)

// PodMetadataPodGVKObject returns a typed object identifying the Pod resource for
// a metadata-only informer.
func PodMetadataPodGVKObject() *metav1.PartialObjectMetadata {
	pod := &metav1.PartialObjectMetadata{}
	pod.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Pod"))
	return pod
}

// NewPodMetadataCache returns a cache that watches only pod metadata (no spec or
// status), retaining just the dapr.io/* annotations of Dapr annotated pods and
// sinkholing everything else. It is used to resolve, server side, which
// configuration is assigned to a connecting sidecar from its pod's dapr.io/config
// annotation, without holding full pod objects in memory.
func NewPodMetadataCache(podOpts PodMetadataCacheOptions) (ctrlcache.Cache, error) {
	cfg := rest.CopyConfig(podOpts.RestConfig)
	cfg.ContentType = runtime.ContentTypeJSON

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	opts := ctrlcache.Options{
		Scheme:     scheme,
		HTTPClient: podOpts.HTTPClient,
		Mapper:     podOpts.Mapper,
		ByObject: map[client.Object]ctrlcache.ByObject{
			PodMetadataPodGVKObject(): {Transform: podMetadataTransform},
		},
	}
	if len(podOpts.Namespace) > 0 {
		opts.DefaultNamespaces = map[string]ctrlcache.Config{podOpts.Namespace: {}}
	}
	if podOpts.SyncPeriod != nil {
		opts.SyncPeriod = podOpts.SyncPeriod
	}

	return ctrlcache.New(cfg, opts)
}

// podMetadataTransform slims cached pod metadata to the minimum the configuration
// hot-reload filter needs: the dapr.io/* annotations. Non-Dapr pods are coalesced
// into a single sinkhole entry.
func podMetadataTransform(i any) (any, error) {
	obj, ok := i.(*metav1.PartialObjectMetadata)
	if !ok { // probably deletedfinalstateunknown
		return i, nil
	}

	if !operatormeta.IsAnnotatedForDapr(obj.GetAnnotations()) {
		return podMetaDevNull, nil
	}

	objClone := obj.DeepCopy()
	objClone.ManagedFields = nil
	objClone.Labels = nil
	objClone.Annotations = daprAnnotations(objClone.Annotations)
	return objClone, nil
}

func newPodMetadata(name, namespace string) *metav1.PartialObjectMetadata {
	pod := PodMetadataPodGVKObject()
	pod.Name = name
	pod.Namespace = namespace
	return pod
}

// daprAnnotations returns only the dapr.io/* annotations from the given map, so
// that pods retained in the cache do not hold on to unrelated (and potentially
// large) annotations such as kubectl's last-applied-configuration. It returns nil
// when there are none.
func daprAnnotations(in map[string]string) map[string]string {
	var out map[string]string
	for k, v := range in {
		if strings.HasPrefix(k, daprAnnotationPrefix) {
			if out == nil {
				out = make(map[string]string, len(in))
			}
			out[k] = v
		}
	}
	return out
}
