/*
Copyright The Kubernetes Authors.

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

// Code generated by informer-gen. DO NOT EDIT.

package v1alpha1

import (
	time "time"

	componentsv1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	versioned "github.com/dapr/dapr/pkg/client/clientset/versioned"
	internalinterfaces "github.com/dapr/dapr/pkg/client/informers/externalversions/internalinterfaces"
	v1alpha1 "github.com/dapr/dapr/pkg/client/listers/components/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// ComponentInformer provides access to a shared informer and lister for
// Components.
type ComponentInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1alpha1.ComponentLister
}

type componentInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewComponentInformer constructs a new informer for Component type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewComponentInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredComponentInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredComponentInformer constructs a new informer for Component type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredComponentInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.ComponentsV1alpha1().Components(namespace).List(options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.ComponentsV1alpha1().Components(namespace).Watch(options)
			},
		},
		&componentsv1alpha1.Component{},
		resyncPeriod,
		indexers,
	)
}

func (f *componentInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredComponentInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *componentInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&componentsv1alpha1.Component{}, f.defaultInformer)
}

func (f *componentInformer) Lister() v1alpha1.ComponentLister {
	return v1alpha1.NewComponentLister(f.Informer().GetIndexer())
}
