// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/phayes/freeport"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const (
	// MiniKubeIPEnvVar is the environment variable name which will have Minikube node IP
	MiniKubeIPEnvVar = "DAPR_TEST_MINIKUBE_IP"

	// PollInterval is how frequently e2e tests will poll for updates.
	PollInterval = 1 * time.Second
	// PollTimeout is how long e2e tests will wait for resource updates when polling.
	PollTimeout = 10 * time.Minute

	// maxReplicas is the maximum replicas of replica sets
	maxReplicas = 10
)

// AppManager holds Kubernetes clients and namespace used for test apps
// and provides the helpers to manage the test apps
type AppManager struct {
	client    *KubeClient
	namespace string
	app       AppDescription

	// stopChannel is the channel used to manage the port forward lifecycle
	stopChannel chan struct{}
	// readyChannel communicates when the tunnel is ready to receive traffic
	readyChannel chan struct{}
}

type PortForwardRequest struct {
	// restConfig is the kubernetes config
	restConfig *rest.Config
	// pod is the selected pod for this port forwarding
	pod apiv1.Pod
	// localPort is the local port that will be selected to forward the PodPort
	localPorts []int
	// podPort is the target port for the pod
	podPorts []int
	// streams configures where to write or read input from
	streams genericclioptions.IOStreams
	// stopChannel is the channel used to manage the port forward lifecycle
	stopChannel chan struct{}
	// stopChannel communicates when the tunnel is ready to receive traffic
	readyChannel chan struct{}
}

// NewAppManager creates AppManager instance
func NewAppManager(kubeClients *KubeClient, namespace string, app AppDescription) *AppManager {
	return &AppManager{
		client:    kubeClients,
		namespace: namespace,
		app:       app,
	}
}

// Name returns app name
func (m *AppManager) Name() string {
	return m.app.AppName
}

// App returns app description
func (m *AppManager) App() AppDescription {
	return m.app
}

// Init installs app by AppDescription
func (m *AppManager) Init() error {
	// Get or create test namespaces
	if _, err := m.GetOrCreateNamespace(); err != nil {
		return err
	}

	// TODO: Dispose app if option is required
	if err := m.Dispose(); err != nil {
		return err
	}

	// Deploy app and wait until deployment is done
	if _, err := m.Deploy(); err != nil {
		return err
	}

	// Wait until app is deployed completely
	if _, err := m.WaitUntilDeploymentState(m.IsDeploymentDone); err != nil {
		return err
	}

	// Validate daprd side car is injected
	if ok, err := m.ValidiateSideCar(); err != nil || ok != m.app.IngressEnabled {
		return err
	}

	// Create Ingress endpoint
	if _, err := m.CreateIngressService(); err != nil {
		return err
	}

	m.readyChannel = make(chan struct{})
	m.stopChannel = make(chan struct{})

	return nil
}

// Dispose deletes deployment and service
func (m *AppManager) Dispose() error {
	if err := m.DeleteDeployment(true); err != nil {
		return err
	}

	if _, err := m.WaitUntilDeploymentState(m.IsDeploymentDeleted); err != nil {
		return err
	}

	if err := m.DeleteService(true); err != nil {
		return err
	}

	if _, err := m.WaitUntilServiceState(m.IsServiceDeleted); err != nil {
		return err
	}

	if m.stopChannel != nil {
		close(m.stopChannel)
	}

	return nil
}

// Deploy deploys app based on app description
func (m *AppManager) Deploy() (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)
	obj := buildDeploymentObject(m.namespace, m.app)

	result, err := deploymentsClient.Create(obj)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// WaitUntilDeploymentState waits until isState returns true
func (m *AppManager) WaitUntilDeploymentState(isState func(*appsv1.Deployment, error) bool) (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)

	var lastDeployment *appsv1.Deployment

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastDeployment, err = deploymentsClient.Get(m.app.AppName, metav1.GetOptions{})
		done := isState(lastDeployment, err)
		if !done && err != nil {
			return true, err
		}
		return done, nil
	})

	if waitErr != nil {
		return nil, fmt.Errorf("deployment %q is not in desired state, received: %+v: %s", m.app.AppName, lastDeployment, waitErr)
	}

	return lastDeployment, nil
}

// IsDeploymentDone returns true if deployment object completes pod deployments
func (m *AppManager) IsDeploymentDone(deployment *appsv1.Deployment, err error) bool {
	return err == nil && deployment.Generation == deployment.Status.ObservedGeneration && deployment.Status.ReadyReplicas == m.app.Replicas && deployment.Status.AvailableReplicas == m.app.Replicas
}

// IsDeploymentDeleted returns true if deployment does not exist or current pod replica is zero
func (m *AppManager) IsDeploymentDeleted(deployment *appsv1.Deployment, err error) bool {
	return err != nil && errors.IsNotFound(err)
}

// ValidiateSideCar validates that dapr side car is running in dapr enabled pods
func (m *AppManager) ValidiateSideCar() (bool, error) {
	if !m.app.DaprEnabled {
		return false, fmt.Errorf("dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)

	// Filter only 'testapp=appName' labeled Pods
	podList, err := podClient.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
	if err != nil {
		return false, err
	}

	if len(podList.Items) != int(m.app.Replicas) {
		return false, fmt.Errorf("expected number of pods for %s: %d, received: %d", m.app.AppName, m.app.Replicas, len(podList.Items))
	}

	// Each pod must have daprd sidecar
	for _, pod := range podList.Items {
		daprdFound := false
		for _, container := range pod.Spec.Containers {
			if container.Name == DaprSideCarName {
				daprdFound = true
			}
		}
		if !daprdFound {
			return false, fmt.Errorf("cannot find dapr sidecar in pod %s", pod.Name)
		}
	}

	return true, nil
}

// DoPortForwarding performs port forwarding for given podname to access test apps in the cluster
func (m *AppManager) DoPortForwarding(podName string, targetPorts ...int) ([]int, error) {
	podClient := m.client.Pods(m.namespace)
	// Filter only 'testapp=appName' labeled Pods
	podList, err := podClient.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})

	if err != nil {
		return nil, err
	}

	name := podName

	// if given pod name is empty , pick the first matching pod name
	if name == "" {
		for _, pod := range podList.Items {
			name = pod.Name
			break
		}
	}

	config := m.client.GetClientConfig()

	var ports []int
	for i := 0; i < len(targetPorts); i++ {
		p, perr := freeport.GetFreePort()
		if perr != nil {
			return nil, perr
		}
		ports = append(ports, p)
	}

	streams := genericclioptions.IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	err = startPortForwarding(PortForwardRequest{
		restConfig: config,
		pod: apiv1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: m.namespace,
			},
		},
		localPorts:   ports,
		podPorts:     targetPorts,
		streams:      streams,
		stopChannel:  m.stopChannel,
		readyChannel: m.readyChannel,
	})

	if err != nil {
		return nil, err
	}

	<-m.readyChannel

	return ports, nil
}

func startPortForwarding(req PortForwardRequest) error {
	// create spdy roundtripper
	roundTripper, upgrader, err := spdy.RoundTripperFor(req.restConfig)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", req.pod.Namespace, req.pod.Name)
	hostIP := strings.TrimLeft(req.restConfig.Host, "htps:/")
	serverURL := url.URL{Scheme: "https", Path: path, Host: hostIP}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)

	var ports []string //nolint: prealloc
	for i, p := range req.podPorts {
		ports = append(ports, fmt.Sprintf("%d:%d", req.localPorts[i], p))
	}
	fw, err := portforward.New(dialer, ports, req.stopChannel, req.readyChannel, req.streams.Out, req.streams.ErrOut)
	if err != nil {
		return err
	}

	go func() {
		if err = fw.ForwardPorts(); err != nil {
			log.Printf("Error closing port fowarding: %+v", err)
			// TODO: How to handle error?
		}
		log.Println("Closed port fowarding")
	}()
	return nil
}

// ScaleDeploymentReplica scales the deployment
func (m *AppManager) ScaleDeploymentReplica(replicas int32) error {
	if replicas < 0 || replicas > maxReplicas {
		return fmt.Errorf("%d is out of range", replicas)
	}

	deploymentsClient := m.client.Deployments(m.namespace)

	scale, err := deploymentsClient.GetScale(m.app.AppName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if scale.Spec.Replicas == replicas {
		return nil
	}

	scale.Spec.Replicas = replicas
	m.app.Replicas = replicas

	_, err = deploymentsClient.UpdateScale(m.app.AppName, scale)

	return err
}

// CreateIngressService creates Ingress endpoint for test app
func (m *AppManager) CreateIngressService() (*apiv1.Service, error) {
	serviceClient := m.client.Services(m.namespace)
	obj := buildServiceObject(m.namespace, m.app)
	result, err := serviceClient.Create(obj)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// AcquireExternalURL gets external ingress endpoint from service when it is ready
func (m *AppManager) AcquireExternalURL() string {
	log.Printf("Waiting until service ingress is ready for %s...\n", m.app.AppName)
	svc, err := m.WaitUntilServiceState(m.IsServiceIngressReady)
	if err != nil {
		return ""
	}

	log.Printf("Service ingress for %s is ready...\n", m.app.AppName)
	return m.AcquireExternalURLFromService(svc)
}

// WaitUntilServiceState waits until isState returns true
func (m *AppManager) WaitUntilServiceState(isState func(*apiv1.Service, error) bool) (*apiv1.Service, error) {
	serviceClient := m.client.Services(m.namespace)
	var lastService *apiv1.Service

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastService, err = serviceClient.Get(m.app.AppName, metav1.GetOptions{})
		done := isState(lastService, err)
		if !done && err != nil {
			return true, err
		}

		return done, nil
	})

	if waitErr != nil {
		return lastService, fmt.Errorf("service %q is not in desired state, received: %+v: %s", m.app.AppName, lastService, waitErr)
	}

	return lastService, nil
}

// AcquireExternalURLFromService gets external url from Service Object.
func (m *AppManager) AcquireExternalURLFromService(svc *apiv1.Service) string {
	if svc.Status.LoadBalancer.Ingress != nil && len(svc.Status.LoadBalancer.Ingress) > 0 && len(svc.Spec.Ports) > 0 {
		address := ""
		if svc.Status.LoadBalancer.Ingress[0].Hostname != "" {
			address = svc.Status.LoadBalancer.Ingress[0].Hostname
		} else {
			address = svc.Status.LoadBalancer.Ingress[0].IP
		}
		return fmt.Sprintf("%s:%d", address, svc.Spec.Ports[0].Port)
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
func (m *AppManager) IsServiceIngressReady(svc *apiv1.Service, err error) bool {
	if err != nil || svc == nil {
		return false
	}

	if svc.Status.LoadBalancer.Ingress != nil && len(svc.Status.LoadBalancer.Ingress) > 0 {
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
func (m *AppManager) IsServiceDeleted(svc *apiv1.Service, err error) bool {
	return err != nil && errors.IsNotFound(err)
}

func (m *AppManager) minikubeNodeIP() string {
	// if you are running the test in minikube environment, DAPR_TEST_MINIKUBE_IP environment variable must be
	// minikube cluster IP address from the output of `minikube ip` command

	// TODO: Use the better way to get the node ip of minikube
	return os.Getenv(MiniKubeIPEnvVar)
}

// DeleteDeployment deletes deployment for the test app
func (m *AppManager) DeleteDeployment(ignoreNotFound bool) error {
	deploymentsClient := m.client.Deployments(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := deploymentsClient.Delete(m.app.AppName, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !errors.IsNotFound(err)) {
		return err
	}

	return nil
}

// DeleteService deletes deployment for the test app
func (m *AppManager) DeleteService(ignoreNotFound bool) error {
	serviceClient := m.client.Services(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := serviceClient.Delete(m.app.AppName, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !errors.IsNotFound(err)) {
		return err
	}

	return nil
}

// GetOrCreateNamespace gets or creates namespace unless namespace exists
func (m *AppManager) GetOrCreateNamespace() (*apiv1.Namespace, error) {
	namespaceClient := m.client.Namespaces()
	ns, err := namespaceClient.Get(m.namespace, metav1.GetOptions{})

	if err != nil && errors.IsNotFound(err) {
		obj := buildNamespaceObject(m.namespace)
		ns, err = namespaceClient.Create(obj)
		return ns, err
	}

	return ns, err
}
