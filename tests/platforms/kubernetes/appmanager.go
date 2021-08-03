// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	PollTimeout = 10 * time.Minute

	// maxReplicas is the maximum replicas of replica sets.
	maxReplicas = 10

	// maxSideCarDetectionRetries is the maximum number of retries to detect Dapr sidecar.
	maxSideCarDetectionRetries = 3
)

// AppManager holds Kubernetes clients and namespace used for test apps
// and provides the helpers to manage the test apps.
type AppManager struct {
	client    *KubeClient
	namespace string
	app       AppDescription

	forwarder *PodPortForwarder

	logPrefix string
}

// PodInfo holds information about a given pod.
type PodInfo struct {
	Name string
	IP   string
}

// NewAppManager creates AppManager instance.
func NewAppManager(kubeClients *KubeClient, namespace string, app AppDescription) *AppManager {
	return &AppManager{
		client:    kubeClients,
		namespace: namespace,
		app:       app,
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
func (m *AppManager) Init() error {
	// Get or create test namespaces
	if _, err := m.GetOrCreateNamespace(); err != nil {
		return err
	}

	// TODO: Dispose app if option is required
	if err := m.Dispose(true); err != nil {
		return err
	}

	m.logPrefix = os.Getenv(ContainerLogPathEnvVar)

	if m.logPrefix == "" {
		m.logPrefix = ContainerLogDefaultPath
	}

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

		if m.logPrefix != "" {
			if err := m.StreamContainerLogs(); err != nil {
				log.Printf("Failed to retrieve container logs for %s. Error was: %s", m.app.AppName, err)
			}
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

		if m.logPrefix != "" {
			if err := m.StreamContainerLogs(); err != nil {
				log.Printf("Failed to retrieve container logs for %s. Error was: %s", m.app.AppName, err)
			}
		}
	}
	log.Printf("App %v has been deployed.", m.app.AppName)

	if !m.app.IsJob {
		// Job cannot have side car validated because it is shutdown on successful completion.
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

		if _, err := m.WaitUntilServiceState(m.IsServiceDeleted); err != nil {
			return err
		}
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

	result, err := jobsClient.Create(context.TODO(), obj, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// WaitUntilJobState waits until isState returns true.
func (m *AppManager) WaitUntilJobState(isState func(*batchv1.Job, error) bool) (*batchv1.Job, error) {
	jobsClient := m.client.Jobs(m.namespace)

	var lastJob *batchv1.Job

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastJob, err = jobsClient.Get(context.TODO(), m.app.AppName, metav1.GetOptions{})
		done := isState(lastJob, err)
		if !done && err != nil {
			return true, err
		}
		return done, nil
	})

	if waitErr != nil {
		return nil, fmt.Errorf("job %q is not in desired state, received: %+v: %s", m.app.AppName, lastJob, waitErr)
	}

	return lastJob, nil
}

// Deploy deploys app based on app description.
func (m *AppManager) Deploy() (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)
	obj := buildDeploymentObject(m.namespace, m.app)

	result, err := deploymentsClient.Create(context.TODO(), obj, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// WaitUntilDeploymentState waits until isState returns true.
func (m *AppManager) WaitUntilDeploymentState(isState func(*appsv1.Deployment, error) bool) (*appsv1.Deployment, error) {
	deploymentsClient := m.client.Deployments(m.namespace)

	var lastDeployment *appsv1.Deployment

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastDeployment, err = deploymentsClient.Get(context.TODO(), m.app.AppName, metav1.GetOptions{})
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
		podList, err := podClient.List(context.TODO(), metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
		})
		podStatus := map[string][]apiv1.ContainerStatus{}
		if err == nil {
			for _, pod := range podList.Items {
				podStatus[pod.Name] = pod.Status.ContainerStatuses
			}
			log.Printf("deployment %s relate pods: %+v", m.app.AppName, podList)
		} else {
			log.Printf("Error list pod for deployment %s. Error was %s", m.app.AppName, err)
		}

		return nil, fmt.Errorf("deployment %q is not in desired state, received: %+v pod status: %+v error: %s", m.app.AppName, lastDeployment, podStatus, waitErr)
	}

	return lastDeployment, nil
}

// WaitUntilSidecarPresent waits until Dapr sidecar is present.
func (m *AppManager) WaitUntilSidecarPresent() error {
	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
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
	return err == nil && deployment.Generation == deployment.Status.ObservedGeneration && deployment.Status.ReadyReplicas == m.app.Replicas && deployment.Status.AvailableReplicas == m.app.Replicas
}

// IsJobDeleted returns true if job does not exist.
func (m *AppManager) IsJobDeleted(job *batchv1.Job, err error) bool {
	return err != nil && errors.IsNotFound(err)
}

// IsDeploymentDeleted returns true if deployment does not exist or current pod replica is zero.
func (m *AppManager) IsDeploymentDeleted(deployment *appsv1.Deployment, err error) bool {
	return err != nil && errors.IsNotFound(err)
}

// ValidateSidecar validates that dapr side car is running in dapr enabled pods.
func (m *AppManager) ValidateSidecar() error {
	if !m.app.DaprEnabled {
		return fmt.Errorf("dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)
	// Filter only 'testapp=appName' labeled Pods
	podList, err := podClient.List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
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
			}
		}
		if !daprdFound {
			return fmt.Errorf("cannot find dapr sidecar in pod %s", pod.Name)
		}
	}

	return nil
}

// getSidecarInfo returns if sidecar is present and how many containers there are.
func (m *AppManager) getContainerInfo() (bool, int, int, error) {
	if !m.app.DaprEnabled {
		return false, 0, 0, fmt.Errorf("dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)

	// Filter only 'testapp=appName' labeled Pods
	podList, err := podClient.List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
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
	podList, err := podClient.List(context.TODO(), metav1.ListOptions{
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

	return m.forwarder.Connect(name, targetPorts...)
}

// ScaleDeploymentReplica scales the deployment.
func (m *AppManager) ScaleDeploymentReplica(replicas int32) error {
	if replicas < 0 || replicas > maxReplicas {
		return fmt.Errorf("%d is out of range", replicas)
	}

	deploymentsClient := m.client.Deployments(m.namespace)

	scale, err := deploymentsClient.GetScale(context.TODO(), m.app.AppName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if scale.Spec.Replicas == replicas {
		return nil
	}

	scale.Spec.Replicas = replicas
	m.app.Replicas = replicas

	_, err = deploymentsClient.UpdateScale(context.TODO(), m.app.AppName, scale, metav1.UpdateOptions{})

	return err
}

// SetAppEnv sets an environment variable.
func (m *AppManager) SetAppEnv(key, value string) error {
	deploymentsClient := m.client.Deployments(m.namespace)

	deployment, err := deploymentsClient.Get(context.TODO(), m.app.AppName, metav1.GetOptions{})
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

	_, err = deploymentsClient.Update(context.TODO(), deployment, metav1.UpdateOptions{})

	return err
}

// CreateIngressService creates Ingress endpoint for test app.
func (m *AppManager) CreateIngressService() (*apiv1.Service, error) {
	serviceClient := m.client.Services(m.namespace)
	obj := buildServiceObject(m.namespace, m.app)
	result, err := serviceClient.Create(context.TODO(), obj, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// AcquireExternalURL gets external ingress endpoint from service when it is ready.
func (m *AppManager) AcquireExternalURL() string {
	log.Printf("Waiting until service ingress is ready for %s...\n", m.app.AppName)
	svc, err := m.WaitUntilServiceState(m.IsServiceIngressReady)
	if err != nil {
		return ""
	}

	log.Printf("Service ingress for %s is ready...\n", m.app.AppName)
	return m.AcquireExternalURLFromService(svc)
}

// WaitUntilServiceState waits until isState returns true.
func (m *AppManager) WaitUntilServiceState(isState func(*apiv1.Service, error) bool) (*apiv1.Service, error) {
	serviceClient := m.client.Services(m.namespace)
	var lastService *apiv1.Service

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		var err error
		lastService, err = serviceClient.Get(context.TODO(), m.app.AppName, metav1.GetOptions{})
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

// IsServiceIngressReady returns true if external ip is available.
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

// IsServiceDeleted returns true if service does not exist.
func (m *AppManager) IsServiceDeleted(svc *apiv1.Service, err error) bool {
	return err != nil && errors.IsNotFound(err)
}

func (m *AppManager) minikubeNodeIP() string {
	// if you are running the test in minikube environment, DAPR_TEST_MINIKUBE_IP environment variable must be
	// minikube cluster IP address from the output of `minikube ip` command

	// TODO: Use the better way to get the node ip of minikube
	return os.Getenv(MiniKubeIPEnvVar)
}

// DeleteJob deletes job for the test app.
func (m *AppManager) DeleteJob(ignoreNotFound bool) error {
	jobsClient := m.client.Jobs(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := jobsClient.Delete(context.TODO(), m.app.AppName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !errors.IsNotFound(err)) {
		return err
	}

	return nil
}

// DeleteDeployment deletes deployment for the test app.
func (m *AppManager) DeleteDeployment(ignoreNotFound bool) error {
	deploymentsClient := m.client.Deployments(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := deploymentsClient.Delete(context.TODO(), m.app.AppName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !errors.IsNotFound(err)) {
		return err
	}

	return nil
}

// DeleteService deletes deployment for the test app.
func (m *AppManager) DeleteService(ignoreNotFound bool) error {
	serviceClient := m.client.Services(m.namespace)
	deletePolicy := metav1.DeletePropagationForeground

	if err := serviceClient.Delete(context.TODO(), m.app.AppName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && (ignoreNotFound && !errors.IsNotFound(err)) {
		return err
	}

	return nil
}

// GetOrCreateNamespace gets or creates namespace unless namespace exists.
func (m *AppManager) GetOrCreateNamespace() (*apiv1.Namespace, error) {
	namespaceClient := m.client.Namespaces()
	ns, err := namespaceClient.Get(context.TODO(), m.namespace, metav1.GetOptions{})

	if err != nil && errors.IsNotFound(err) {
		obj := buildNamespaceObject(m.namespace)
		ns, err = namespaceClient.Create(context.TODO(), obj, metav1.CreateOptions{})
		return ns, err
	}

	return ns, err
}

// GetHostDetails returns the name and IP address of the pods running the app.
func (m *AppManager) GetHostDetails() ([]PodInfo, error) {
	if !m.app.DaprEnabled {
		return nil, fmt.Errorf("dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)

	// Filter only 'testapp=appName' labeled Pods
	podList, err := podClient.List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
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

// SaveContainerLogs get container logs for all containers in the pod and saves them to disk.
func (m *AppManager) StreamContainerLogs() error {
	podClient := m.client.Pods(m.namespace)

	// Filter only 'testapp=appName' labeled Pods
	podList, err := podClient.List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
	if err != nil {
		return err
	}

	for _, pod := range podList.Items {
		for _, container := range pod.Spec.Containers {
			go func(pod, container string) {
				filename := fmt.Sprintf("%s/%s.%s.log", m.logPrefix, pod, container)
				log.Printf("Streaming Kubernetes logs to %s", filename)
				req := podClient.GetLogs(pod, &apiv1.PodLogOptions{
					Container: container,
					Follow:    true,
				})
				stream, err := req.Stream(context.TODO())
				if err != nil {
					log.Printf("Error reading log stream for %s. Error was %s", filename, err)
					return
				}
				defer stream.Close()

				fh, err := os.Create(filename)
				if err != nil {
					log.Printf("Error creating %s. Error was %s", filename, err)
					return
				}
				defer fh.Close()

				for {
					buf := make([]byte, 2000)
					numBytes, err := stream.Read(buf)
					if numBytes == 0 {
						continue
					}

					if err == io.EOF {
						break
					}

					if err != nil {
						log.Printf("Error reading log stream for %s. Error was %s", filename, err)
						return
					}

					_, err = fh.Write(buf[:numBytes])
					if err != nil {
						log.Printf("Error writing to %s. Error was %s", filename, err)
						return
					}
				}

				log.Printf("Saved container logs to %s", filename)
			}(pod.GetName(), container.Name)
		}
	}

	return nil
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
		metrics, err := m.client.MetricsClient.MetricsV1beta1().PodMetricses(m.namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil {
			return -1, -1, err
		}

		for _, c := range metrics.Containers {
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
	}
	if (maxCPU < 0) || (maxMemory < 0) {
		return -1, -1, fmt.Errorf("container (sidecar=%v) not found in pods for app %s in namespace %s", sidecar, m.app.AppName, m.namespace)
	}

	return maxCPU, maxMemory, nil
}

// GetTotalRestarts returns the total number of restarts for the app or sidecar.
func (m *AppManager) GetTotalRestarts() (int, error) {
	if !m.app.DaprEnabled {
		return 0, fmt.Errorf("dapr is not enabled for this app")
	}

	podClient := m.client.Pods(m.namespace)

	// Filter only 'testapp=appName' labeled Pods
	podList, err := podClient.List(context.TODO(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, m.app.AppName),
	})
	if err != nil {
		return 0, err
	}

	restartCount := 0
	for _, pod := range podList.Items {
		pod, err := podClient.Get(context.TODO(), pod.GetName(), metav1.GetOptions{})
		if err != nil {
			return 0, err
		}

		for _, containerStatus := range pod.Status.ContainerStatuses {
			restartCount += int(containerStatus.RestartCount)
		}
	}

	return restartCount, nil
}
