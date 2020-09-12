// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DaprComponent holds kubernetes client and component information
type DaprComponent struct {
	namespace  string
	kubeClient *KubeClient
	component  ComponentDescription
}

// NewDaprComponent creates DaprComponent instance
func NewDaprComponent(client *KubeClient, ns string, comp ComponentDescription) *DaprComponent {
	return &DaprComponent{
		namespace:  ns,
		kubeClient: client,
		component:  comp,
	}
}

func (do *DaprComponent) addComponent() (*v1alpha1.Component, error) {
	client := do.kubeClient.DaprComponents(DaprTestNamespace)

	metadata := []v1alpha1.MetadataItem{}

	for k, v := range do.component.MetaData {
		metadata = append(metadata, v1alpha1.MetadataItem{
			Name:  k,
			Value: v,
		})
	}

	obj := buildDaprComponentObject(do.component.Name, do.component.TypeName, metadata)
	return client.Create(obj)
}

func (do *DaprComponent) deleteComponent() error {
	client := do.kubeClient.DaprComponents(DaprTestNamespace)
	return client.Delete(do.component.Name, &metav1.DeleteOptions{})
}

func (do *DaprComponent) Name() string {
	return do.component.Name
}

func (do *DaprComponent) Init() error {
	_, err := do.addComponent()
	return err
}

func (do *DaprComponent) Dispose(wait bool) error {
	return do.deleteComponent()
}
