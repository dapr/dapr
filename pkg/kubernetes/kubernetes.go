// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"github.com/dapr/dapr/utils"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type KubernetesAPI struct {
	kubeClient kubernetes.Interface
}

// New returns a new Kubernetes Client
func New(c kubernetes.Interface) *KubernetesAPI {
	var nc kubernetes.Interface
	if c == nil {
		nc = utils.GetKubeClient()
	} else {
		nc = c
	}

	return &KubernetesAPI{
		kubeClient: nc,
	}
}

// GetDeployment gets a deployment
func (k *KubernetesAPI) GetDeployment(name, namespace string) (*appsv1.Deployment, error) {
	dep, err := k.kubeClient.AppsV1().Deployments(namespace).Get(name, meta_v1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return dep, nil
}

// UpdateDeployment updates an existing deployment
func (k *KubernetesAPI) UpdateDeployment(deployment *appsv1.Deployment) error {
	_, err := k.kubeClient.AppsV1().Deployments(deployment.ObjectMeta.Namespace).Update(deployment)
	if err != nil {
		return err
	}

	return nil
}

// CreateService creates a new service
func (k *KubernetesAPI) CreateService(service *corev1.Service, namespace string) error {
	_, err := k.kubeClient.CoreV1().Services(namespace).Create(service)
	if err != nil {
		return err
	}

	return nil
}

// Delete a service
func (k *KubernetesAPI) DeleteService(serviceName string, namespace string) error {
	return k.kubeClient.CoreV1().Services(namespace).Delete(serviceName, &meta_v1.DeleteOptions{})
}

// ServiceExists checks if a service already exists
func (k *KubernetesAPI) ServiceExists(name, namespace string) bool {
	_, err := k.kubeClient.CoreV1().Services(namespace).Get(name, meta_v1.GetOptions{})
	return err == nil
}

// GetEndpoints returns a list of service endpoints
func (k *KubernetesAPI) GetEndpoints(name, namespace string) (*corev1.Endpoints, error) {
	endpoints, err := k.kubeClient.CoreV1().Endpoints(namespace).Get(name, meta_v1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return endpoints, nil
}

// GetDeploymentsBySelector returns a deployment by a selector
func (k *KubernetesAPI) GetDeploymentsBySelector(selector meta_v1.LabelSelector) ([]appsv1.Deployment, error) {
	s := labels.SelectorFromSet(selector.MatchLabels)

	dep, err := k.kubeClient.AppsV1().Deployments(meta_v1.NamespaceAll).List(meta_v1.ListOptions{
		LabelSelector: s.String(),
	})
	if err != nil {
		return nil, err
	}

	return dep.Items, nil
}
