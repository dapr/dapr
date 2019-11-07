// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"fmt"
	"os"
	"time"

	"istio.io/pkg/log"

	"github.com/dapr/dapr/tests/utils"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// MiniKubeIPEnvVar is the environment variable name which will have Minikube node IP
	MiniKubeIPEnvVar = "MINIKUBE_IP"

	// PollInterval is how frequently e2e tests will poll for updates.
	PollInterval = 1 * time.Second
	// PollTimeout is how long e2e tests will wait for resource updates when polling.
	PollTimeout = 10 * time.Minute
)

// AppManager holds Kubernetes clients and namespace used for test apps
// and provides the helpers to manage the test apps
type AppManager struct {
	client    *KubeClient
	namespace string
}

// NewAppManager creates AppUtil instance
func NewAppManager(kubeClients *KubeClient, namespace string) *AppManager {
	return &AppManager{
		client:    kubeClients,
		namespace: namespace,
	}
}

// Deploy deploys test app based on app deployment information
func (m *AppManager) Deploy(app utils.AppDescription) (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)
	obj := BuildDeploymentObject(m.namespace, app)

	result, err := deploymentsClient.Create(obj)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// WaitUntilDeploymentIsDone waits until test app deployment is done
func (m *AppManager) WaitUntilDeploymentIsDone(app utils.AppDescription) (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)

	var lastDeployment *appsv1.Deployment

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastDeployment, err = deploymentsClient.Get(app.AppName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return m.isDeploymentDone(lastDeployment, app.Replicas), nil
	})

	if waitErr != nil {
		return nil, fmt.Errorf("deployment %q is not in desired state, got: %+v: %w", app.AppName, lastDeployment, waitErr)
	}

	return lastDeployment, nil
}

func (m *AppManager) isDeploymentDone(deployment *appsv1.Deployment, replicas int32) bool {
	return deployment.Generation == deployment.Status.ObservedGeneration && deployment.Status.ReadyReplicas == replicas
}

// ValidiateSideCar validates that dapr side car is running in dapr enabled pods
func (m *AppManager) ValidiateSideCar(app utils.AppDescription) (bool, error) {
	if !app.DaprEnabled {
		return false, fmt.Errorf("Dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)

	// Filter only 'testapp=appName' labeled Pods
	podList, err := podClient.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, app.AppName),
	})
	if err != nil {
		return false, err
	}

	if len(podList.Items) != int(app.Replicas) {
		return false, fmt.Errorf("Number of Pods for %s must be %d Pods but %d Pods", app.AppName, app.Replicas, len(podList.Items))
	}

	// All testapp pods must have daprd sidecar
	for _, pod := range podList.Items {
		daprdFound := false
		for _, container := range pod.Spec.Containers {
			if container.Name == DaprSideCarName {
				daprdFound = true
			}
		}
		if !daprdFound {
			return false, fmt.Errorf("Cannot find dapr sidecar in %s pod", pod.Name)
		}
	}

	return true, nil
}

// CreateIngressService creates Ingress endpoint for test app
func (m *AppManager) CreateIngressService(app utils.AppDescription) error {
	if !app.IngressEnabled {
		return fmt.Errorf("%s doesn't need ingress service endpoint", app.AppName)
	}

	serviceClient := m.client.Services(m.namespace)
	obj := BuildServiceObject(m.namespace, app)
	result, err := serviceClient.Create(obj)
	if err != nil {
		return err
	}

	log.Debugf("Created Service %q.\n", result.GetObjectMeta().GetName())

	return nil
}

// WaitUntilIngressEndpointIsAvailable waits until ingress external ip is available
func (m *AppManager) WaitUntilIngressEndpointIsAvailable(app utils.AppDescription) (string, error) {
	if !app.IngressEnabled {
		return "", fmt.Errorf("%s doesn't need ingress service endpoint", app.AppName)
	}

	minikubeExternalIP := m.minikubeNodeIP()

	serviceClient := m.client.Services(m.namespace)
	var lastService *apiv1.Service
	var externalURL string

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastService, err = serviceClient.Get(app.AppName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		svcSpec := lastService.Spec

		if len(svcSpec.ExternalIPs) > 0 {
			externalURL = svcSpec.ExternalIPs[0]
			return true, nil
		} else if minikubeExternalIP != "" {
			// if test cluster is minikube, external ip address is minikube node address
			if len(svcSpec.Ports) > 0 {
				externalURL = fmt.Sprintf("%s:%d", minikubeExternalIP, svcSpec.Ports[0].NodePort)
				return true, nil
			}
		}

		return false, nil
	})

	if waitErr != nil {
		return externalURL, fmt.Errorf("service %q is not in desired state, got: %+v: %w", app.AppName, lastService, waitErr)
	}

	return externalURL, nil
}

func (m *AppManager) minikubeNodeIP() string {
	return os.Getenv(MiniKubeIPEnvVar)
}

// Cleanup deletes deployment and service
func (m *AppManager) Cleanup(app utils.AppDescription) error {
	if err := m.DeleteDeployment(app); err != nil {
		return err
	}

	if err := m.DeleteService(app); err != nil {
		return err
	}

	return nil
}

// DeleteDeployment deletes deployment for the test app
func (m *AppManager) DeleteDeployment(app utils.AppDescription) error {
	deploymentsClient := m.client.Deployments(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := deploymentsClient.Delete(app.AppName, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		return err
	}

	return nil
}

// DeleteService deletes deployment for the test app
func (m *AppManager) DeleteService(app utils.AppDescription) error {
	serviceClient := m.client.Services(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := serviceClient.Delete(app.AppName, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		return err
	}

	return nil
}
