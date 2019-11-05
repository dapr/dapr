// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/utils"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type Clients struct {
	kubeClient kubernetes.Interface
	daprClient scheme.Interface
}

// NewClients returns a type containing new Kubernetes Client and Dapr Client
func NewClients(kubeClient kubernetes.Interface, daprClient scheme.Interface) (*Clients, error) {
	var kc kubernetes.Interface
	if kubeClient == nil {
		kc = utils.GetKubeClient()
	} else {
		kc = kubeClient
	}

	var dc scheme.Interface
	var err error
	if daprClient == nil {
		config := utils.GetConfig()

		dc, err = scheme.NewForConfig(config)
	} else {
		dc = daprClient
	}

	if err != nil {
		return nil, err
	}

	return &Clients{
		kubeClient: kc,
		daprClient: dc,
	}, nil
}

// GetDeployment gets a deployment
func (c *Clients) GetDeployment(name, namespace string) (*appsv1.Deployment, error) {
	dep, err := c.kubeClient.AppsV1().Deployments(namespace).Get(name, meta_v1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return dep, nil
}

// UpdateDeployment updates an existing deployment
func (c *Clients) UpdateDeployment(deployment *appsv1.Deployment) error {
	_, err := c.kubeClient.AppsV1().Deployments(deployment.ObjectMeta.Namespace).Update(deployment)
	if err != nil {
		return err
	}

	return nil
}

// CreateService creates a new service
func (c *Clients) CreateService(service *corev1.Service, namespace string) error {
	_, err := c.kubeClient.CoreV1().Services(namespace).Create(service)
	if err != nil {
		return err
	}

	return nil
}

// Delete a service
func (c *Clients) DeleteService(serviceName string, namespace string) error {
	return c.kubeClient.CoreV1().Services(namespace).Delete(serviceName, &meta_v1.DeleteOptions{})
}

// ServiceExists checks if a service already exists
func (c *Clients) ServiceExists(name, namespace string) bool {
	_, err := c.kubeClient.CoreV1().Services(namespace).Get(name, meta_v1.GetOptions{})
	return err == nil
}

// GetEndpoints returns a list of service endpoints
func (c *Clients) GetEndpoints(name, namespace string) (*corev1.Endpoints, error) {
	endpoints, err := c.kubeClient.CoreV1().Endpoints(namespace).Get(name, meta_v1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return endpoints, nil
}

// GetDeploymentsBySelector returns a deployment by a selector
func (c *Clients) GetDeploymentsBySelector(selector meta_v1.LabelSelector) ([]appsv1.Deployment, error) {
	s := labels.SelectorFromSet(selector.MatchLabels)

	dep, err := c.kubeClient.AppsV1().Deployments(meta_v1.NamespaceAll).List(meta_v1.ListOptions{
		LabelSelector: s.String(),
	})
	if err != nil {
		return nil, err
	}

	return dep.Items, nil
}

// GetDaprClient returns Dapr Client
func (c *Clients) GetDaprClient() scheme.Interface {
	return c.daprClient
}

// GetKubeClient returns Kube Client
func (c *Clients) GetKubeClient() kubernetes.Interface {
	return c.kubeClient
}
