/*
Copyright 2021 The Dapr Authors
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
	"context"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

// DaprComponent holds kubernetes client and component information.
type DaprComponent struct {
	namespace  string
	kubeClient *KubeClient
	component  ComponentDescription
}

// NewDaprComponent creates DaprComponent instance.
func NewDaprComponent(client *KubeClient, ns string, comp ComponentDescription) *DaprComponent {
	return &DaprComponent{
		namespace:  ns,
		kubeClient: client,
		component:  comp,
	}
}

// toComponentSpec builds the componentSpec for the given ComponentDescription
func (do *DaprComponent) toComponentSpec() *v1alpha1.Component {
	metadata := []v1alpha1.MetadataItem{}

	for k, v := range do.component.MetaData {
		var item v1alpha1.MetadataItem

		if v.FromSecretRef == nil {
			item = v1alpha1.MetadataItem{
				Name: k,
				Value: v1alpha1.DynamicValue{
					JSON: v1.JSON{
						Raw: []byte(v.Raw),
					},
				},
			}
		} else {
			item = v1alpha1.MetadataItem{
				Name: k,
				SecretKeyRef: v1alpha1.SecretKeyRef{
					Name: v.FromSecretRef.Name,
					Key:  v.FromSecretRef.Key,
				},
			}
		}
		metadata = append(metadata, item)
	}

	annotations := make(map[string]string)
	if do.component.ContainerAsJSON != "" {
		annotations["dapr.io/component-container"] = do.component.ContainerAsJSON
	}

	return buildDaprComponentObject(do.component.Name, do.component.TypeName, do.component.Scopes, annotations, metadata)
}

func (do *DaprComponent) addComponent() (*v1alpha1.Component, error) {
	return do.kubeClient.DaprComponents(DaprTestNamespace).Create(do.toComponentSpec())
}

func (do *DaprComponent) deleteComponent() error {
	client := do.kubeClient.DaprComponents(DaprTestNamespace)
	return client.Delete(do.component.Name, &metav1.DeleteOptions{})
}

func (do *DaprComponent) Name() string {
	return do.component.Name
}

func (do *DaprComponent) Init(ctx context.Context) error {
	_, err := do.addComponent()
	return err
}

func (do *DaprComponent) Dispose(wait bool) error {
	return do.deleteComponent()
}
