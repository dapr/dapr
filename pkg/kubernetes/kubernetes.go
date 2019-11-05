// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type Client struct {
	kubeClient kubernetes.Interface
	daprClient scheme.Interface
}

// NewClient returns a type containing new Kubernetes Client and Dapr Client
func NewClient(kubeClient kubernetes.Interface, daprClient scheme.Interface) *Client {
	return &Client{
		kubeClient: kubeClient,
		daprClient: daprClient,
	}
}

// GetDeployment gets a deployment
func (c *Client) GetDeployment(name, namespace string) (*appsv1.Deployment, error) {
	dep, err := c.kubeClient.AppsV1().Deployments(namespace).Get(name, meta_v1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return dep, nil
}

// UpdateDeployment updates an existing deployment
func (c *Client) UpdateDeployment(deployment *appsv1.Deployment) error {
	_, err := c.kubeClient.AppsV1().Deployments(deployment.ObjectMeta.Namespace).Update(deployment)
	if err != nil {
		return err
	}

	return nil
}

// CreateService creates a new service
func (c *Client) CreateService(service *corev1.Service, namespace string) error {
	_, err := c.kubeClient.CoreV1().Services(namespace).Create(service)
	if err != nil {
		return err
	}

	return nil
}

// Delete a service
func (c *Client) DeleteService(serviceName string, namespace string) error {
	return c.kubeClient.CoreV1().Services(namespace).Delete(serviceName, &meta_v1.DeleteOptions{})
}

// ServiceExists checks if a service already exists
func (c *Client) ServiceExists(name, namespace string) bool {
	_, err := c.kubeClient.CoreV1().Services(namespace).Get(name, meta_v1.GetOptions{})
	return err == nil
}

// GetEndpoints returns a list of service endpoints
func (c *Client) GetEndpoints(name, namespace string) (*corev1.Endpoints, error) {
	endpoints, err := c.kubeClient.CoreV1().Endpoints(namespace).Get(name, meta_v1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return endpoints, nil
}

// GetDeploymentsBySelector returns a deployment by a selector
func (c *Client) GetDeploymentsBySelector(selector meta_v1.LabelSelector) ([]appsv1.Deployment, error) {
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
func (c *Client) GetDaprClient() scheme.Interface {
	return c.daprClient
}

// GetKubeClient returns Kube Client
func (c *Client) GetKubeClient() kubernetes.Interface {
	return c.kubeClient
}
