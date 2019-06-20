package kubernetes

import (
	actions_v1alpha1 "github.com/actionscore/actions/pkg/apis/eventing/v1alpha1"
	scheme "github.com/actionscore/actions/pkg/client/clientset/versioned"
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

func EventSourcesIndexInformer(
	client scheme.Interface,
	namespace string,
	fieldSelector fields.Selector,
	labelSelector labels.Selector,
) cache.SharedIndexInformer {
	actionsClient := client.EventingV1alpha1().EventSources(namespace)
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				if fieldSelector != nil {
					options.FieldSelector = fieldSelector.String()
				}
				if labelSelector != nil {
					options.LabelSelector = labelSelector.String()
				}
				return actionsClient.List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				if fieldSelector != nil {
					options.FieldSelector = fieldSelector.String()
				}
				if labelSelector != nil {
					options.LabelSelector = labelSelector.String()
				}
				return actionsClient.Watch(options)
			},
		},
		&actions_v1alpha1.EventSource{},
		0,
		cache.Indexers{},
	)
}
