// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"fmt"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/dapr/dapr/tests/utils"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// MiniKubeIPEnvVar is the environment variable name which will have Minikube node IP
	MiniKubeIPEnvVar = "DAPR_TEST_MINIKUBE_IP"

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

// NewAppManager creates AppManager instance
func NewAppManager(kubeClients *KubeClient, namespace string) *AppManager {
	return &AppManager{
		client:    kubeClients,
		namespace: namespace,
	}
}

// Deploy deploys app based on app description
func (m *AppManager) Deploy(app utils.AppDescription) (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)
	obj := BuildDeploymentObject(m.namespace, app)

	result, err := deploymentsClient.Create(obj)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// WaitUntilDeploymentState waits until isState returns true
func (m *AppManager) WaitUntilDeploymentState(app utils.AppDescription, isState func(*appsv1.Deployment, error, *utils.AppDescription) bool) (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)

	var lastDeployment *appsv1.Deployment

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastDeployment, err = deploymentsClient.Get(app.AppName, metav1.GetOptions{})
		done := isState(lastDeployment, err, &app)
		if !done && err != nil {
			return true, err
		}
		return done, nil
	})

	if waitErr != nil {
		return nil, fmt.Errorf("deployment %q is not in desired state, got: %+v: %w", app.AppName, lastDeployment, waitErr)
	}

	return lastDeployment, nil
}

// IsDeploymentDone returns true if deployment object completes pod deployments
func (m *AppManager) IsDeploymentDone(deployment *appsv1.Deployment, err error, app *utils.AppDescription) bool {
	return err == nil && deployment.Generation == deployment.Status.ObservedGeneration && deployment.Status.ReadyReplicas == app.Replicas
}

// IsDeploymentDeleted returns true if deployment does not exist or current pod replica is zero
func (m *AppManager) IsDeploymentDeleted(deployment *appsv1.Deployment, err error, app *utils.AppDescription) bool {
	return errors.IsNotFound(err) || deployment.Status.Replicas == 0
}

// ValidiateSideCar validates that dapr side car is running in dapr enabled pods
func (m *AppManager) ValidiateSideCar(app utils.AppDescription) (bool, error) {
	if !app.DaprEnabled {
		return false, fmt.Errorf("dapr is not enabled for this app")
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
		return false, fmt.Errorf("number of Pods for %s must be %d Pods but %d Pods", app.AppName, app.Replicas, len(podList.Items))
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
			return false, fmt.Errorf("cannot find dapr sidecar in %s pod", pod.Name)
		}
	}

	return true, nil
}

// CreateIngressService creates Ingress endpoint for test app
func (m *AppManager) CreateIngressService(app utils.AppDescription) (*apiv1.Service, error) {
	if !app.IngressEnabled {
		return nil, fmt.Errorf("%s doesn't need ingress service endpoint", app.AppName)
	}

	serviceClient := m.client.Services(m.namespace)
	obj := BuildServiceObject(m.namespace, app)
	result, err := serviceClient.Create(obj)
	if err != nil {
		return nil, err
	}

	log.Debugf("Created Service %q.\n", result.GetObjectMeta().GetName())

	return result, nil
}

// WaitUntilServiceState waits until isState returns true
func (m *AppManager) WaitUntilServiceState(app utils.AppDescription, isState func(*apiv1.Service, error, *utils.AppDescription) bool) (*apiv1.Service, error) {
	serviceClient := m.client.Services(m.namespace)
	var lastService *apiv1.Service

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastService, err = serviceClient.Get(app.AppName, metav1.GetOptions{})
		done := isState(lastService, err, &app)
		if !done && err != nil {
			return true, err
		}

		return done, nil
	})

	if waitErr != nil {
		return lastService, fmt.Errorf("service %q is not in desired state, got: %+v: %w", app.AppName, lastService, waitErr)
	}

	return lastService, nil
}

// AcquireExternalURLFromService gets external url from Service Object.
func (m *AppManager) AcquireExternalURLFromService(svc *apiv1.Service) string {
	if len(svc.Spec.ExternalIPs) > 0 {
		return svc.Spec.ExternalIPs[0]
	}

	// TODO: Support the other local k8s clusters
	if minikubeExternalIP := m.minikubeNodeIP(); minikubeExternalIP != "" {
		// if test cluster is minikube, external ip address is minikube node address
		if len(svc.Spec.Ports) > 0 {
			return fmt.Sprintf("%s:%d", minikubeExternalIP, svc.Spec.Ports[0].NodePort)
		}
	}

	return ""
}

// IsServiceIngressReady returns true if external ip is available
func (m *AppManager) IsServiceIngressReady(svc *apiv1.Service, err error, app *utils.AppDescription) bool {
	if err != nil || svc == nil {
		return false
	}

	if len(svc.Spec.ExternalIPs) > 0 {
		return true
	}

	// TODO: Support the other local k8s clusters
	if m.minikubeNodeIP() != "" {
		if len(svc.Spec.Ports) > 0 {
			return true
		}
	}

	return false
}

// IsServiceDeleted returns true if service does not exist
func (m *AppManager) IsServiceDeleted(svc *apiv1.Service, err error, app *utils.AppDescription) bool {
	return errors.IsNotFound(err)
}

func (m *AppManager) minikubeNodeIP() string {
	// if you are running the test in minikube environment, DAPR_TEST_MINIKUBE_IP environment variable must be
	// minikube cluster IP address from the output of `minikube ip` command

	// TODO: Use the better way to get the node ip of minikube
	return os.Getenv(MiniKubeIPEnvVar)
}

// Cleanup deletes deployment and service
func (m *AppManager) Cleanup(app utils.AppDescription) error {
	if err := m.DeleteDeployment(app, true); err != nil {
		return err
	}

	if _, err := m.WaitUntilDeploymentState(app, m.IsDeploymentDeleted); err != nil {
		return err
	}

	if err := m.DeleteService(app, true); err != nil {
		return err
	}

	if _, err := m.WaitUntilServiceState(app, m.IsServiceDeleted); err != nil {
		return err
	}

	return nil
}

// DeleteDeployment deletes deployment for the test app
func (m *AppManager) DeleteDeployment(app utils.AppDescription, ignoreNotFound bool) error {
	deploymentsClient := m.client.Deployments(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := deploymentsClient.Delete(app.AppName, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !errors.IsNotFound(err)) {
		return err
	}

	return nil
}

// DeleteService deletes deployment for the test app
func (m *AppManager) DeleteService(app utils.AppDescription, ignoreNotFound bool) error {
	serviceClient := m.client.Services(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := serviceClient.Delete(app.AppName, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !errors.IsNotFound(err)) {
		return err
	}

	return nil
}
