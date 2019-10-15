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

// Clients returns a new Kubernetes and Dapr clients
func Clients() (kubernetes.Interface, scheme.Interface, error) {
	client := utils.GetKubeClient()
	config := utils.GetConfig()

	eventingClient, err := scheme.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	return client, eventingClient, nil
}

// GetDeployment gets a deployment
func GetDeployment(name, namespace string) (*appsv1.Deployment, error) {
	client := utils.GetKubeClient()

	dep, err := client.AppsV1().Deployments(namespace).Get(name, meta_v1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return dep, nil
}

// UpdateDeployment updates an existing deployment
func UpdateDeployment(deployment *appsv1.Deployment) error {
	client := utils.GetKubeClient()
	_, err := client.AppsV1().Deployments(deployment.ObjectMeta.Namespace).Update(deployment)
	if err != nil {
		return err
	}

	return nil
}

// CreateService creates a new service
func CreateService(service *corev1.Service, namespace string) error {
	client := utils.GetKubeClient()

	_, err := client.CoreV1().Services(namespace).Create(service)
	if err != nil {
		return err
	}

	return nil
}

// ServiceExists checks if a service already exists
func ServiceExists(name, namespace string) bool {
	client := utils.GetKubeClient()

	_, err := client.CoreV1().Services(namespace).Get(name, meta_v1.GetOptions{})
	return err == nil
}

// GetEndpoints returns a list of service endpoints
func GetEndpoints(name, namespace string) (*corev1.Endpoints, error) {
	client := utils.GetKubeClient()

	endpoints, err := client.CoreV1().Endpoints(namespace).Get(name, meta_v1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return endpoints, nil
}

// GetDeploymentsBySelector returns a deployment by a selector
func GetDeploymentsBySelector(selector meta_v1.LabelSelector) ([]appsv1.Deployment, error) {
	client := utils.GetKubeClient()

	s := labels.SelectorFromSet(selector.MatchLabels)

	dep, err := client.AppsV1().Deployments(meta_v1.NamespaceAll).List(meta_v1.ListOptions{
		LabelSelector: s.String(),
	})
	if err != nil {
		return nil, err
	}

	return dep.Items, nil
}
