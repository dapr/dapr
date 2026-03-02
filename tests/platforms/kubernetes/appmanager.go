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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// MiniKubeIPEnvVar is the environment variable name which will have Minikube node IP.
	MiniKubeIPEnvVar = "DAPR_TEST_MINIKUBE_IP"

	// ContainerLogPathEnvVar is the environment variable name which will have the container logs.
	ContainerLogPathEnvVar = "DAPR_CONTAINER_LOG_PATH"

	// ContainerLogDefaultPath.
	ContainerLogDefaultPath = "./container_logs"

	// PollInterval is how frequently e2e tests will poll for updates.
	PollInterval = 1 * time.Second
	// PollTimeout is how long e2e tests will wait for resource updates when polling.
	PollTimeout = 8 * time.Minute

	// maxReplicas is the maximum replicas of replica sets.
	maxReplicas = 10

	// maxSideCarDetectionRetries is the maximum number of retries to detect Dapr sidecar.
	maxSideCarDetectionRetries = 3

	// KindDefaultContextPrefix is the prefix of the default kind cluster
	KindDefaultContextPrefix = "kind-"

	// kindDefaultContextName is the name of the default kind cluster
	kindDefaultContextName = "kind"

	// kindClusterNameEnvVar is the environment variable name which will have the name of the kind cluster.
	kindClusterNameEnvVar = "DAPR_TEST_KIND_CLUSTER_NAME"
)

// AppManager holds Kubernetes clients and namespace used for test apps
// and provides the helpers to manage the test apps.
type AppManager struct {
	client    *KubeClient
	namespace string
	app       AppDescription
	ctx       context.Context

	forwarder     *PodPortForwarder
	forwarderUsed bool

	logPrefix string
}

// PodInfo holds information about a given pod.
type PodInfo struct {
	Name string
	IP   string
}

// NewAppManager creates AppManager instance.
func NewAppManager(client *KubeClient, namespace string, app AppDescription) *AppManager {
	return &AppManager{
		client:    client,
		namespace: namespace,
		app:       app,
		ctx:       context.Background(),
	}
}

// Name returns app name.
func (m *AppManager) Name() string {
	return m.app.AppName
}

// App returns app description.
func (m *AppManager) App() AppDescription {
	return m.app
}

// Init installs app by AppDescription.
func (m *AppManager) Init(runCtx context.Context) error {
	m.ctx = runCtx

	// TODO: Dispose app if option is required
	if err := m.Dispose(true); err != nil {
		return err
	}

	m.logPrefix = logPrefix

	if err := os.MkdirAll(m.logPrefix, os.ModePerm); err != nil {
		log.Printf("Failed to create output log directory '%s' Error was: '%s'. Container logs will be discarded", m.logPrefix, err)
		m.logPrefix = ""
	}

	log.Printf("Deploying app %v ...", m.app.AppName)
	if m.app.IsJob {
		// Deploy app and wait until deployment is done
		if _, err := m.ScheduleJob(); err != nil {
			return err
		}

		// Wait until app is deployed completely
		if _, err := m.WaitUntilJobState(m.IsJobCompleted); err != nil {
			return err
		}
	} else {
		// Deploy app and wait until deployment is done
		if _, err := m.Deploy(); err != nil {
			return err
		}

		// Wait until app is deployed completely
		if _, err := m.WaitUntilDeploymentState(m.IsDeploymentDone); err != nil {
			return err
		}
	}
	log.Printf("App %v has been deployed.", m.app.AppName)

	if m.logPrefix != "" {
		if err := m.StreamContainerLogs(); err != nil {
			log.Printf("Failed to retrieve container logs for %s. Error was: %s", m.app.AppName, err)
		}
	}

	if !m.app.IsJob {
		// Job cannot have side car validated because it is shutdown on successful completion.
		if m.app.DaprEnabled {
			log.Printf("Validating sidecar for app %v ....", m.app.AppName)
			for i := 0; i <= maxSideCarDetectionRetries; i++ {
				// Validate daprd side car is injected
				if err := m.ValidateSidecar(); err != nil {
					if i == maxSideCarDetectionRetries {
						return err
					}

					log.Printf("Did not find sidecar for app %v error %s, retrying ....", m.app.AppName, err)
					time.Sleep(10 * time.Second)
					continue
				}

				break
			}
			log.Printf("Sidecar for app %v has been validated.", m.app.AppName)
		}

		// Create Ingress endpoint
		log.Printf("Creating ingress for app %v ....", m.app.AppName)
		if _, err := m.CreateIngressService(); err != nil {
			return err
		}
		log.Printf("Ingress for app %v has been created.", m.app.AppName)

		log.Printf("Creating pod port forwarder for app %v ....", m.app.AppName)
		m.forwarder = NewPodPortForwarder(m.client, m.namespace)
		log.Printf("Pod port forwarder for app %v has been created.", m.app.AppName)
	}

	return nil
}

// Dispose deletes deployment and service.
func (m *AppManager) Dispose(wait bool) error {
	if m.app.IsJob {
		if err := m.DeleteJob(true); err != nil {
			return err
		}
	} else {
		if err := m.DeleteDeployment(true); err != nil {
			return err
		}
	}

	if err := m.DeleteService(true); err != nil {
		return err
	}

	if wait {
		if m.app.IsJob {
			if _, err := m.WaitUntilJobState(m.IsJobDeleted); err != nil {
				return err
			}
		} else {
			if _, err := m.WaitUntilDeploymentState(m.IsDeploymentDeleted); err != nil {
				return err
			}
		}

		if _, err := m.WaitUntilServiceState(m.app.AppName, m.IsServiceDeleted); err != nil {
			return err
		}
	} else {
		// Wait 2 seconds for logs to come in
		time.Sleep(2 * time.Second)
	}

	if m.forwarder != nil {
		m.forwarder.Close()
	}

	return nil
}

// ScheduleJob deploys job based on app description.
func (m *AppManager) ScheduleJob() (*batchv1.Job, error) {
	jobsClient := m.client.Jobs(m.namespace)
	obj := buildJobObject(m.namespace, m.app)

	ctx, cancel := context.WithTimeout(m.ctx, time.Minute)
	result, err := jobsClient.Create(ctx, obj, metav1.CreateOptions{})
	cancel()
	if err != nil {
		return nil, err
	}

	return result, nil
}

// WaitUntilJobState waits until isState returns true.
func (m *AppManager) WaitUntilJobState(isState func(*batchv1.Job, error) bool) (*batchv1.Job, error) {
	jobsClient := m.client.Jobs(m.namespace)

	var lastJob *batchv1.Job

	ctx, cancel := context.WithTimeout(m.ctx, PollTimeout)
	defer cancel()

	waitErr := wait.PollUntilContextCancel(ctx, PollInterval, true, func(ctx context.Context) (bool, error) {
		var err error
		lastJob, err = jobsClient.Get(ctx, m.app.AppName, metav1.GetOptions{})
		done := isState(lastJob, err)
		if !done && err != nil {
			return true, err
		}
		return done, nil
	})

	if waitErr != nil {
		// Try to get the logs from the containers that aren't starting, if we can get anything
		// We ignore errors here
		_ = m.StreamContainerLogs()

		return nil, fmt.Errorf("job %q is not in desired state, received: %#v: %s", m.app.AppName, lastJob, waitErr)
	}

	return lastJob, nil
}

// Deploy deploys app based on app description.
func (m *AppManager) Deploy() (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)
	obj := buildDeploymentObject(m.namespace, m.app)

	ctx, cancel := context.WithTimeout(m.ctx, time.Minute)
	result, err := deploymentsClient.Create(ctx, obj, metav1.CreateOptions{})
	cancel()
	if err != nil {
		return nil, err
	}

	return result, nil
}

// WaitUntilDeploymentState waits until isState returns true.
func (m *AppManager) WaitUntilDeploymentState(isState func(*appsv1.Deployment, error) bool) (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)

	var lastDeployment *appsv1.Deployment

	ctx, cancel := context.WithTimeout(m.ctx, PollTimeout)
	defer cancel()

	waitErr := wait.PollUntilContextCancel(ctx, PollInterval, true, func(ctx context.Context) (bool, error) {
		var err error
		lastDeployment, err = deploymentsClient.Get(ctx, m.app.AppName, metav1.GetOptions{})
		done := isState(lastDeployment, err)
		if !done && err != nil {
			return true, err
		}
		return done, nil
	})

	if waitErr != nil {
		// get deployment's Pods detail status info
		podClient := m.client.Pods(m.namespace)
		// Filter only 'testapp=appName' labeled Pods
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		podList, err := podClient.List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
		})
		// Reset Spec and ObjectMeta which could contain sensitive info like credentials
		lastDeployment.Spec.Reset()
		lastDeployment.ObjectMeta.Reset()
		podStatus := map[string][]apiv1.ContainerStatus{}
		if err == nil {
			for i, pod := range podList.Items {
				name := pod.Name
				podStatus[name] = pod.Status.ContainerStatuses
				request := podClient.GetLogs(name, &apiv1.PodLogOptions{
					Container: DaprSideCarName,
					Previous:  true,
				})
				var body []byte
				if body, err = request.DoRaw(context.Background()); err != nil {
					log.Printf("(%s) get previous pod log failed. err: %s\n", name, err.Error())
				}
				log.Printf("previous pod: %s, logs: %s\n", name, string(body))
				// Reset Spec and ObjectMeta which could contain sensitive info like credentials
				pod.Spec.Reset()
				pod.ObjectMeta.Reset()
				podList.Items[i] = pod
			}
			j, _ := json.Marshal(podList)
			log.Printf("deployment %s relate pods: %s", m.app.AppName, string(j))
		} else {
			log.Printf("Error list pod for deployment %s. Error was %s", m.app.AppName, err)
		}

		// Try to get the logs from the containers that aren't starting, if we can get anything
		// We ignore errors here
		_ = m.StreamContainerLogs()

		return nil, fmt.Errorf("deployment %q is not in desired state, received: %#v pod status: %#v error: %s", m.app.AppName, lastDeployment, podStatus, waitErr)
	}

	return lastDeployment, nil
}

// WaitUntilSidecarPresent waits until Dapr sidecar is present.
func (m *AppManager) WaitUntilSidecarPresent() error {
	ctx, cancel := context.WithTimeout(m.ctx, PollTimeout)
	defer cancel()

	waitErr := wait.PollUntilContextCancel(ctx, PollInterval, true, func(ctx context.Context) (bool, error) {
		allDaprd, minContainerCount, maxContainerCount, err := m.getContainerInfo()
		log.Printf(
			"Checking if Dapr sidecar is present on app %s (minContainerCount=%d, maxContainerCount=%d, allDaprd=%v): %v ...",
			m.app.AppName,
			minContainerCount,
			maxContainerCount,
			allDaprd,
			err)
		return allDaprd, err
	})

	if waitErr != nil {
		return fmt.Errorf("app %q does not contain Dapr sidecar", m.app.AppName)
	}

	return nil
}

// IsJobCompleted returns true if job object is complete.
func (m *AppManager) IsJobCompleted(job *batchv1.Job, err error) bool {
	return err == nil && job.Status.Succeeded == 1 && job.Status.Failed == 0 && job.Status.Active == 0 && job.Status.CompletionTime != nil
}

// IsDeploymentDone returns true if deployment object completes pod deployments.
func (m *AppManager) IsDeploymentDone(deployment *appsv1.Deployment, err error) bool {
	return err == nil &&
		deployment.Generation == deployment.Status.ObservedGeneration &&
		deployment.Status.ReadyReplicas == m.app.Replicas &&
		deployment.Status.AvailableReplicas == m.app.Replicas
}

// IsJobDeleted returns true if job does not exist.
func (m *AppManager) IsJobDeleted(job *batchv1.Job, err error) bool {
	return err != nil && apierrors.IsNotFound(err)
}

// IsDeploymentDeleted returns true if deployment does not exist or current pod replica is zero.
func (m *AppManager) IsDeploymentDeleted(deployment *appsv1.Deployment, err error) bool {
	return err != nil && apierrors.IsNotFound(err)
}

// ValidateSidecar validates that dapr side car is running in dapr enabled pods.
func (m *AppManager) ValidateSidecar() error {
	if !m.app.DaprEnabled {
		return errors.New("dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)
	// Filter only 'testapp=appName' labeled Pods
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	podList, err := podClient.List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
	cancel()
	if err != nil {
		return err
	}

	if len(podList.Items) != int(m.app.Replicas) {
		return fmt.Errorf("expected number of pods for %s: %d, received: %d", m.app.AppName, m.app.Replicas, len(podList.Items))
	}

	// Each pod must have daprd sidecar
	for _, pod := range podList.Items {
		daprdFound := false
		for _, container := range pod.Spec.Containers {
			if container.Name == DaprSideCarName {
				daprdFound = true
				break
			}
		}

		if !daprdFound {
			found, _ := json.Marshal(pod.Spec.Containers)
			return fmt.Errorf("cannot find dapr sidecar in pod %s. Found containers=%v", pod.Name, string(found))
		}
	}

	return nil
}

// getSidecarInfo returns if sidecar is present and how many containers there are.
func (m *AppManager) getContainerInfo() (bool, int, int, error) {
	if !m.app.DaprEnabled {
		return false, 0, 0, errors.New("dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)

	// Filter only 'testapp=appName' labeled Pods
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	podList, err := podClient.List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
	cancel()
	if err != nil {
		return false, 0, 0, err
	}

	// Each pod must have daprd sidecar
	minContainerCount := -1
	maxContainerCount := 0
	allDaprd := true && (len(podList.Items) > 0)
	for _, pod := range podList.Items {
		daprdFound := false
		containerCount := len(pod.Spec.Containers)
		if containerCount < minContainerCount || minContainerCount == -1 {
			minContainerCount = containerCount
		}
		if containerCount > maxContainerCount {
			maxContainerCount = containerCount
		}

		for _, container := range pod.Spec.Containers {
			if container.Name == DaprSideCarName {
				daprdFound = true
			}
		}

		if !daprdFound {
			allDaprd = false
		}
	}

	if minContainerCount < 0 {
		minContainerCount = 0
	}

	return allDaprd, minContainerCount, maxContainerCount, nil
}

// DoPortForwarding performs port forwarding for given podname to access test apps in the cluster.
func (m *AppManager) DoPortForwarding(podName string, targetPorts ...int) ([]int, error) {
	podClient := m.client.Pods(m.namespace)
	// Filter only 'testapp=appName' labeled Pods
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	podList, err := podClient.List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
	cancel()
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

	if m.forwarderUsed {
		log.Printf("Re-creating pod port forwarder for app %v ....", m.app.AppName)
		m.forwarder.Close()
		m.forwarder = NewPodPortForwarder(m.client, m.namespace)
		log.Printf("Pod port forwarder for app %v has been created.", m.app.AppName)
	}

	m.forwarderUsed = true

	return m.forwarder.Connect(name, targetPorts...)
}

// ScaleDeploymentReplica scales the deployment.
func (m *AppManager) ScaleDeploymentReplica(replicas int32) error {
	if replicas < 0 || replicas > maxReplicas {
		return fmt.Errorf("%d is out of range", replicas)
	}

	deploymentsClient := m.client.Deployments(m.namespace)

	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	scale, err := deploymentsClient.GetScale(ctx, m.app.AppName, metav1.GetOptions{})
	cancel()
	if err != nil {
		return err
	}

	if scale.Spec.Replicas == replicas {
		return nil
	}

	scale.Spec.Replicas = replicas
	m.app.Replicas = replicas

	ctx, cancel = context.WithTimeout(m.ctx, 30*time.Second)
	_, err = deploymentsClient.UpdateScale(ctx, m.app.AppName, scale, metav1.UpdateOptions{})
	cancel()

	return err
}

// SetAppEnv sets an environment variable.
func (m *AppManager) SetAppEnv(key, value string) error {
	deploymentsClient := m.client.Deployments(m.namespace)

	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	deployment, err := deploymentsClient.Get(ctx, m.app.AppName, metav1.GetOptions{})
	cancel()
	if err != nil {
		return err
	}

	for i, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name != DaprSideCarName {
			found := false
			for j, envName := range deployment.Spec.Template.Spec.Containers[i].Env {
				if envName.Name == key {
					deployment.Spec.Template.Spec.Containers[i].Env[j].Value = value
					found = true
					break
				}
			}

			if !found {
				deployment.Spec.Template.Spec.Containers[i].Env = append(
					deployment.Spec.Template.Spec.Containers[i].Env,
					apiv1.EnvVar{
						Name:  key,
						Value: value,
					},
				)
			}
			break
		}
	}

	ctx, cancel = context.WithTimeout(m.ctx, 30*time.Second)
	_, err = deploymentsClient.Update(ctx, deployment, metav1.UpdateOptions{})
	cancel()

	return err
}

// CreateIngressService creates Ingress endpoint for test app.
func (m *AppManager) CreateIngressService() (*apiv1.Service, error) {
	serviceClient := m.client.Services(m.namespace)
	obj := buildServiceObject(m.namespace, m.app)
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	result, err := serviceClient.Create(ctx, obj, metav1.CreateOptions{})
	cancel()
	if err != nil {
		return nil, err
	}

	return result, nil
}

// AcquireExternalURL gets external ingress endpoint from service when it is ready.
func (m *AppManager) AcquireExternalURL() string {
	log.Printf("Waiting until service ingress is ready for %s...\n", m.app.AppName)
	svc, err := m.WaitUntilServiceState(m.app.AppName, m.IsServiceIngressReady)
	if err != nil {
		log.Printf("Service ingress for %s is not ready: %s", m.app.AppName, err)
		return ""
	}

	url := m.AcquireExternalURLFromService(svc)
	log.Printf("Service ingress for %s is ready...: url=%s\n", m.app.AppName, url)
	return url
}

// WaitUntilServiceState waits until isState returns true.
func (m *AppManager) WaitUntilServiceState(svcName string, isState func(*apiv1.Service, error) bool) (*apiv1.Service, error) {
	serviceClient := m.client.Services(m.namespace)
	var lastService *apiv1.Service

	ctx, cancel := context.WithTimeout(m.ctx, PollTimeout)
	defer cancel()

	waitErr := wait.PollUntilContextCancel(ctx, PollInterval, true, func(ctx context.Context) (bool, error) {
		var err error
		lastService, err = serviceClient.Get(ctx, svcName, metav1.GetOptions{})
		done := isState(lastService, err)
		if !done && err != nil {
			log.Printf("wait for %s: %s", svcName, err)
			return true, err
		}

		return done, nil
	})

	if waitErr != nil {
		return lastService, fmt.Errorf("service %q is not in desired state, received: %#v: %s", m.app.AppName, lastService, waitErr)
	}

	return lastService, nil
}

// AcquireExternalURLFromService gets external url from Service Object.
func (m *AppManager) AcquireExternalURLFromService(svc *apiv1.Service) string {
	svcPorts := svc.Spec.Ports
	if len(svcPorts) == 0 {
		return ""
	}

	svcFstPort, svcIngress := svcPorts[0], svc.Status.LoadBalancer.Ingress
	// the default service address is the internal one
	address, port := svc.Spec.ClusterIP, svcFstPort.Port
	if svcIngress != nil && len(svcIngress) > 0 {
		if svcIngress[0].Hostname != "" {
			address = svcIngress[0].Hostname
		} else {
			address = svcIngress[0].IP
		}
		// TODO: Support the other local k8s clusters
	} else if minikubeExternalIP := m.minikubeNodeIP(); minikubeExternalIP != "" {
		// if test cluster is minikube, external ip address is minikube node address
		address, port = minikubeExternalIP, svcFstPort.NodePort
	} else if m.isKindCluster() && m.isCurrentContextLocal() {
		// Support Kind cluster with specific name that is used in the `make setup-kind` target
		host := m.getCurrentContextHost()
		log.Printf("Running in Kind cluster, forwarding ports to %s", host)

		// if test cluster is kind, "external" ip address is localhost or 127.0.0.1, and we need to forward port
		ports, err := m.DoPortForwarding("", int(svcFstPort.Port))
		if err != nil {
			log.Printf("error forwarding ports: %s", err)
			return ""
		}

		address, port = host, int32(ports[0]) //nolint:gosec
	}
	return fmt.Sprintf("%s:%d", address, port)
}

// IsServiceIngressReady returns true if external ip is available.
func (m *AppManager) IsServiceIngressReady(svc *apiv1.Service, err error) bool {
	if err != nil || svc == nil {
		return false
	}

	if svc.Status.LoadBalancer.Ingress != nil && len(svc.Status.LoadBalancer.Ingress) > 0 {
		return true
	}

	if len(svc.Spec.Ports) > 0 {
		// TODO: Support the other local k8s clusters
		return m.minikubeNodeIP() != "" || !m.app.ShouldBeExposed() || (m.isKindCluster() && m.isCurrentContextLocal())
	}

	return false
}

// IsServiceDeleted returns true if service does not exist.
func (m *AppManager) IsServiceDeleted(svc *apiv1.Service, err error) bool {
	return err != nil && apierrors.IsNotFound(err)
}

func (m *AppManager) minikubeNodeIP() string {
	// if you are running the test in minikube environment, DAPR_TEST_MINIKUBE_IP environment variable must be
	// minikube cluster IP address from the output of `minikube ip` command

	// TODO: Use the better way to get the node ip of minikube
	return os.Getenv(MiniKubeIPEnvVar)
}

func (m *AppManager) isKindCluster() bool {
	customClusterName := os.Getenv(kindClusterNameEnvVar)
	if customClusterName != "" {
		return m.client.RawConfig.CurrentContext == KindDefaultContextPrefix+customClusterName
	}
	return m.client.RawConfig.CurrentContext == KindDefaultContextPrefix+kindDefaultContextName
}

func (m *AppManager) isCurrentContextLocal() bool {
	host := m.getCurrentContextHost()
	if host == "" {
		return false
	}

	ip := net.ParseIP(host)
	return ip.IsLoopback()
}

func (m *AppManager) getCurrentContextHost() string {
	currentContextName := m.client.RawConfig.CurrentContext
	currentContext := m.client.RawConfig.Contexts[currentContextName]
	currentCluster := m.client.RawConfig.Clusters[currentContext.Cluster]

	serverAddressHost, err := url.Parse(currentCluster.Server)
	if err != nil {
		log.Printf("error parsing server url: %s, error: %+v", currentCluster.Server, err)
		return ""
	}

	host, _, err := net.SplitHostPort(serverAddressHost.Host)
	if err != nil {
		log.Printf("error splitting host and port: %s, error: %+v", serverAddressHost.Host, err)
		return ""
	}
	return host
}

// DeleteJob deletes job for the test app.
func (m *AppManager) DeleteJob(ignoreNotFound bool) error {
	jobsClient := m.client.Jobs(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	if err := jobsClient.Delete(ctx, m.app.AppName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !apierrors.IsNotFound(err)) {
		return err
	}

	return nil
}

// DeleteDeployment deletes deployment for the test app.
func (m *AppManager) DeleteDeployment(ignoreNotFound bool) error {
	deploymentsClient := m.client.Deployments(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	if err := deploymentsClient.Delete(ctx, m.app.AppName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !apierrors.IsNotFound(err)) {
		return err
	}

	return nil
}

// DeleteService deletes service for the test app.
func (m *AppManager) DeleteService(ignoreNotFound bool) error {
	serviceClient := m.client.Services(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	if err := serviceClient.Delete(ctx, m.app.AppName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !apierrors.IsNotFound(err)) {
		return err
	}

	return nil
}

// GetHostDetails returns the name and IP address of the pods running the app.
func (m *AppManager) GetHostDetails() ([]PodInfo, error) {
	if !m.app.DaprEnabled {
		return nil, errors.New("dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)

	// Filter only 'testapp=appName' labeled Pods
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	podList, err := podClient.List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
	cancel()
	if err != nil {
		return nil, err
	}

	if len(podList.Items) != int(m.app.Replicas) {
		return nil, fmt.Errorf("expected number of pods for %s: %d, received: %d", m.app.AppName, m.app.Replicas, len(podList.Items))
	}

	result := make([]PodInfo, 0, len(podList.Items))
	for _, item := range podList.Items {
		result = append(result, PodInfo{
			Name: item.GetName(),
			IP:   item.Status.PodIP,
		})
	}

	return result, nil
}

// StreamContainerLogs get container logs for all containers in the pod and saves them to disk.
func (m *AppManager) StreamContainerLogs() error {
	return StreamContainerLogsToDisk(m.ctx, m.app.AppName, m.client.Pods(m.namespace))
}

// GetCPUAndMemory returns the Cpu and Memory usage for the dapr app or sidecar.
func (m *AppManager) GetCPUAndMemory(sidecar bool) (int64, float64, error) {
	pods, err := m.GetHostDetails()
	if err != nil {
		return -1, -1, err
	}

	var maxCPU int64 = -1
	var maxMemory float64 = -1
	for _, pod := range pods {
		podName := pod.Name
		// The metrics-server scrapes on a ~60s interval and may not yet have data
		// for a recently started or restarted pod. Retry on NotFound for up to 60s
		metricsCtx, metricsCancel := context.WithTimeout(m.ctx, 60*time.Second)
		waitErr := wait.PollUntilContextCancel(metricsCtx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			podMetrics, getErr := m.client.MetricsClient.MetricsV1beta1().PodMetricses(m.namespace).Get(ctx, podName, metav1.GetOptions{})
			if getErr != nil {
				if apierrors.IsNotFound(getErr) {
					log.Printf("Pod metrics for %s not yet available, retrying...", podName)
					return false, nil
				}
				return false, getErr
			}
			for _, c := range podMetrics.Containers {
				isSidecar := c.Name == DaprSideCarName
				if isSidecar == sidecar {
					mi, _ := c.Usage.Memory().AsInt64()
					mb := float64((mi / 1024)) * 0.001024

					cpu := c.Usage.Cpu().ScaledValue(resource.Milli)

					if cpu > maxCPU {
						maxCPU = cpu
					}

					if mb > maxMemory {
						maxMemory = mb
					}
				}
			}
			return true, nil
		})
		metricsCancel()
		if waitErr != nil {
			return -1, -1, waitErr
		}
	}
	if (maxCPU < 0) || (maxMemory < 0) {
		return -1, -1, fmt.Errorf("container (sidecar=%v) not found in pods for app %s in namespace %s", sidecar, m.app.AppName, m.namespace)
	}

	return maxCPU, maxMemory, nil
}

// GetTotalRestarts returns the total number of restarts for the app or sidecar.
func (m *AppManager) GetTotalRestarts() (int, error) {
	if !m.app.DaprEnabled {
		return 0, errors.New("dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)

	// Filter only 'testapp=appName' labeled Pods
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	podList, err := podClient.List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
	cancel()
	if err != nil {
		return 0, err
	}

	restartCount := 0
	for _, pod := range podList.Items {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		pod, err := podClient.Get(ctx, pod.GetName(), metav1.GetOptions{})
		cancel()
		if err != nil {
			return 0, err
		}

		for _, containerStatus := range pod.Status.ContainerStatuses {
			restartCount += int(containerStatus.RestartCount)
		}
	}

	return restartCount, nil
}
