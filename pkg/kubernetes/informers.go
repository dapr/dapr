// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

func DeploymentsIndexInformer(
	client kubernetes.Interface,
	namespace string,
	fieldSelector fields.Selector,
	labelSelector labels.Selector,
) cache.SharedIndexInformer {
	deploymentsClient := client.AppsV1().Deployments(namespace)
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				if fieldSelector != nil {
					options.FieldSelector = fieldSelector.String()
				}
				if labelSelector != nil {
					options.LabelSelector = labelSelector.String()
				}
				return deploymentsClient.List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				if fieldSelector != nil {
					options.FieldSelector = fieldSelector.String()
				}
				if labelSelector != nil {
					options.LabelSelector = labelSelector.String()
				}
				return deploymentsClient.Watch(options)
			},
		},
		&appsv1.Deployment{},
		0,
		cache.Indexers{},
	)
}

func ComponentsIndexInformer(
	client scheme.Interface,
	namespace string,
	fieldSelector fields.Selector,
	labelSelector labels.Selector,
) cache.SharedIndexInformer {
	daprClient := client.ComponentsV1alpha1().Components(namespace)
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				if fieldSelector != nil {
					options.FieldSelector = fieldSelector.String()
				}
				if labelSelector != nil {
					options.LabelSelector = labelSelector.String()
				}
				return daprClient.List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				if fieldSelector != nil {
					options.FieldSelector = fieldSelector.String()
				}
				if labelSelector != nil {
					options.LabelSelector = labelSelector.String()
				}
				return daprClient.Watch(options)
			},
		},
		&components_v1alpha1.Component{},
		0,
		cache.Indexers{},
	)
}
