// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type API struct {
	kubeClient kubernetes.Interface
	daprClient scheme.Interface
}

// NewAPI returns api to interact with kubernetes
func NewAPI(kubeClient kubernetes.Interface, daprClient scheme.Interface) *API {
	return &API{
		kubeClient: kubeClient,
		daprClient: daprClient,
	}
}

// GetDeployment gets a deployment
func (a *API) GetDeployment(name, namespace string) (*appsv1.Deployment, error) {
	dep, err := a.kubeClient.AppsV1().Deployments(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return dep, nil
}

// UpdateDeployment updates an existing deployment
func (a *API) UpdateDeployment(deployment *appsv1.Deployment) error {
	_, err := a.kubeClient.AppsV1().Deployments(deployment.ObjectMeta.Namespace).Update(deployment)
	return err
}

// CreateService creates a new service
func (a *API) CreateService(service *corev1.Service, namespace string) error {
	_, err := a.kubeClient.CoreV1().Services(namespace).Create(service)
	return err
}

// Delete a service
func (a *API) DeleteService(serviceName string, namespace string) error {
	return a.kubeClient.CoreV1().Services(namespace).Delete(serviceName, &metav1.DeleteOptions{})
}

// ServiceExists checks if a service already exists
func (a *API) ServiceExists(name, namespace string) bool {
	_, err := a.kubeClient.CoreV1().Services(namespace).Get(name, metav1.GetOptions{})
	return err == nil
}

// GetEndpoints returns a list of service endpoints
func (a *API) GetEndpoints(name, namespace string) (*corev1.Endpoints, error) {
	endpoints, err := a.kubeClient.CoreV1().Endpoints(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return endpoints, nil
}

// GetDeploymentsBySelector returns a deployment by a selector
func (a *API) GetDeploymentsBySelector(selector metav1.LabelSelector) ([]appsv1.Deployment, error) {
	s := labels.SelectorFromSet(selector.MatchLabels)

	dep, err := a.kubeClient.AppsV1().Deployments(metav1.NamespaceAll).List(metav1.ListOptions{
		LabelSelector: s.String(),
	})
	if err != nil {
		return nil, err
	}

	return dep.Items, nil
}

// GetDaprClient returns Dapr Client
func (a *API) GetDaprClient() scheme.Interface {
	return a.daprClient
}

// GetKubeClient returns Kube Client
func (a *API) GetKubeClient() kubernetes.Interface {
	return a.kubeClient
}
