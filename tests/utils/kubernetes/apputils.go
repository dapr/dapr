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

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// PollInterval is how frequently e2e tests will poll for updates.
	PollInterval = 1 * time.Second
	// PollTimeout is how long e2e tests will wait for resource updates when polling.
	PollTimeout = 10 * time.Minute
	// MiniKubeIPEnv is the environment variable name which will have Minikube node IP
	MiniKubeIPEnv = "MINIKUBE_IP"
)

// AppDescription holds the deployment information of test app
type AppDescription struct {
	AppName        string
	DaprEnabled    bool
	ImageName      string
	RegistryName   string
	Replicas       int32
	IngressEnabled bool
}

// AppUtils holds Kubernetes clients and namespace used for test apps
// and provides the helpers to manage the test apps
type AppUtils struct {
	client    *KubeClient
	namespace string
}

// NewAppUtils creates AppUtil instance
func NewAppUtils(kubeClients *KubeClient, namespace string) *AppUtils {
	return &AppUtils{
		client:    kubeClients,
		namespace: namespace,
	}
}

// DeployApp deploys test app based on appDesc deployment information
func (au *AppUtils) DeployApp(appDesc AppDescription) error {
	deploymentsClient := au.client.Deployments(au.namespace)
	obj := BuildDeploymentObject(au.namespace, appDesc)

	_, err := deploymentsClient.Create(obj)
	if err != nil {
		return err
	}

	return nil
}

// WaitUntilDeploymentReady waits until test app deployment is done
func (au *AppUtils) WaitUntilDeploymentReady(appDesc AppDescription) (*appsv1.Deployment, error) {
	deploymentsClient := au.client.Deployments(au.namespace)

	var lastDeployment *appsv1.Deployment

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastDeployment, err = deploymentsClient.Get(appDesc.AppName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return au.isDeploymentDone(lastDeployment, appDesc.Replicas), nil
	})

	if waitErr != nil {
		return nil, fmt.Errorf("deployment %q is not in desired state, got: %+v: %w", appDesc.AppName, lastDeployment, waitErr)
	}

	return lastDeployment, nil
}

func (au *AppUtils) isDeploymentDone(deployment *appsv1.Deployment, replicas int32) bool {
	return deployment.Generation == deployment.Status.ObservedGeneration && deployment.Status.ReadyReplicas == replicas
}

// ValdiateDaprSideCar validates that dapr side car is running in dapr enabled pods
func (au *AppUtils) ValdiateDaprSideCar(appDesc AppDescription) (bool, error) {
	if !appDesc.DaprEnabled {
		return false, fmt.Errorf("Dapr is not enabled for this app")
	}

	podClient := au.client.Pods(au.namespace)
	podList, err := podClient.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, appDesc.AppName),
	})
	if err != nil {
		return false, err
	}

	if len(podList.Items) != int(appDesc.Replicas) {
		return false, fmt.Errorf("Number of Pods for %s must be %d Pods but %d Pods", appDesc.AppName, appDesc.Replicas, len(podList.Items))
	}

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
func (au *AppUtils) CreateIngressService(appDesc AppDescription) error {
	if !appDesc.IngressEnabled {
		return fmt.Errorf("%s doesn't need ingress service endpoint", appDesc.AppName)
	}

	serviceClient := au.client.Services(au.namespace)
	obj := BuildServiceObject(au.namespace, appDesc)
	result, err := serviceClient.Create(obj)
	if err != nil {
		return err
	}

	log.Debugf("Created Service %q.\n", result.GetObjectMeta().GetName())

	return nil
}

// WaitUntilIngressEndpointIsAvailable waits until ingress external ip is available
func (au *AppUtils) WaitUntilIngressEndpointIsAvailable(appDesc AppDescription) (string, error) {
	serviceClient := au.client.Services(au.namespace)

	minikubeExternalIP := os.Getenv(MiniKubeIPEnv)

	var lastService *apiv1.Service
	var externalURL string

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastService, err = serviceClient.Get(appDesc.AppName, metav1.GetOptions{})
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
		return externalURL, fmt.Errorf("service %q is not in desired state, got: %+v: %w", appDesc.AppName, lastService, waitErr)
	}

	return externalURL, nil
}

// CleanupApp deletes deployment and service
func (au *AppUtils) CleanupApp(appDesc AppDescription) error {
	if err := au.DeleteDeployment(appDesc); err != nil {
		return err
	}

	if err := au.DeleteService(appDesc); err != nil {
		return err
	}

	return nil
}

// DeleteDeployment deletes deployment for the test app
func (au *AppUtils) DeleteDeployment(appDesc AppDescription) error {
	deploymentsClient := au.client.Deployments(au.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := deploymentsClient.Delete(appDesc.AppName, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		return err
	}

	return nil
}

// DeleteService deletes deployment for the test app
func (au *AppUtils) DeleteService(appDesc AppDescription) error {
	serviceClient := au.client.Services(au.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := serviceClient.Delete(appDesc.AppName, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		return err
	}

	return nil
}
