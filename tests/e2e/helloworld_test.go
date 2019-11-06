package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/utils"
	"github.com/dapr/dapr/tests/utils/kubernetes"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"

	log "github.com/Sirupsen/logrus"
)

const (
	appName         = "helloworld"
	TestAppLabelKey = "testapp"
	DaprSideCarName = "daprd"
	// PollInterval is how frequently e2e tests will poll for updates.
	PollInterval = 1 * time.Second
	// PollTimeout is how long e2e tests will wait for resource updates when polling.
	PollTimeout = 10 * time.Minute
)

func TestHelloWorld(t *testing.T) {
	kubeClient, err := kubernetes.NewKubeClients("", "")
	if err != nil {
		log.Info("test")
	}

	DeployApp(kubeClient, appName)
	WaitUntilDeploymentReady(kubeClient, appName)
	ValdiateDaprSideCar(kubeClient, appName, 1)

	CreateService(kubeClient, appName)
	CleanupTestApp(kubeClient, appName)
}

func DeployApp(client *kubernetes.KubeClients, name string) error {
	deploymentsClient := client.ClientSet.AppsV1().Deployments(utils.DaprTestKubeNameSpace)

	result, err := deploymentsClient.Create(constructDeploymentDefinition(name))
	if err != nil {
		return err
	}

	log.Debugf("Created deployment %q.\n", result.GetObjectMeta().GetName())

	return nil
}

func constructDeploymentDefinition(name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: utils.DaprTestKubeNameSpace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					TestAppLabelKey: name,
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						TestAppLabelKey: name,
					},
					Annotations: map[string]string{
						"dapr.io/enabled": "true",
						"dapr.io/id":      name,
						"dapr.io/port":    "3000",
					},
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:  name,
							Image: "youngp/e2e-helloworld",
							Ports: []apiv1.ContainerPort{
								{
									Name:          "http",
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: 3000,
								},
							},
						},
					},
				},
			},
		},
	}
}

func WaitUntilDeploymentReady(client *kubernetes.KubeClients, name string) error {
	deploymentsClient := client.ClientSet.AppsV1().Deployments(utils.DaprTestKubeNameSpace)

	var lastDeployment *appsv1.Deployment

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		lastDeployment, err := deploymentsClient.Get(name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return isDeploymentDone(lastDeployment, 1), nil
	})

	if waitErr != nil {
		return fmt.Errorf("deployment %q is not in desired state, got: %+v: %w", name, lastDeployment, waitErr)
	}

	return nil
}

func isDeploymentDone(deployment *appsv1.Deployment, replicas int32) bool {
	return deployment.Generation == deployment.Status.ObservedGeneration && deployment.Status.ReadyReplicas == replicas
}

func ValdiateDaprSideCar(client *kubernetes.KubeClients, name string, numPod int) (bool, error) {
	podClient := client.ClientSet.CoreV1().Pods(utils.DaprTestKubeNameSpace)
	podList, err := podClient.List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", TestAppLabelKey, name),
	})
	if err != nil {
		return false, err
	}

	if len(podList.Items) != numPod {
		return false, fmt.Errorf("Number of Pods for %s must be %d Pods but %d Pods", name, numPod, len(podList.Items))
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

func CreateService(client *kubernetes.KubeClients, name string) error {
	serviceClient := client.ClientSet.CoreV1().Services(utils.DaprTestKubeNameSpace)
	result, err := serviceClient.Create(constructServiceDefinition(name))
	if err != nil {
		return err
	}

	log.Debugf("Created Service %q.\n", result.GetObjectMeta().GetName())

	return nil
}

func WaitAndGetExternalIP(client *kubernetes.KubeClients, name string) (string, error) {
	serviceClient := client.ClientSet.CoreV1().Services(utils.DaprTestKubeNameSpace)

	minikubeExternalIP := os.Getenv("MINIKUBE_IP")

	var lastService *apiv1.Service
	var externalURL string

	waitErr := wait.PollImmediate(PollInterval, PollTimeout, func() (bool, error) {
		lastService, err := serviceClient.Get(name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		svcSpec := lastService.Spec
		hasExternalIP := false

		if len(svcSpec.ExternalIPs) > 0 {
			externalURL = svcSpec.ExternalIPs[0]
			hasExternalIP = true
		}

		if !hasExternalIP && minikubeExternalIP != "" {
			// if test cluster is minikube, external ip address is minikube node address
			if len(svcSpec.Ports) > 0 {
				externalURL = fmt.Sprintf("%s:%d", minikubeExternalIP, svcSpec.Ports[0].NodePort)
				hasExternalIP = true
			}
		}

		return hasExternalIP, fmt.Errorf("External IP is not assigned")
	})

	if waitErr != nil {
		return externalURL, fmt.Errorf("service %q is not in desired state, got: %+v: %w", name, lastService, waitErr)
	}

	return externalURL, nil
}

func constructServiceDefinition(name string) *apiv1.Service {
	return &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: utils.DaprTestKubeNameSpace,
			Labels: map[string]string{
				TestAppLabelKey: name,
			},
		},
		Spec: apiv1.ServiceSpec{
			Selector: map[string]string{
				TestAppLabelKey: name,
			},
			Ports: []apiv1.ServicePort{
				{
					Protocol:   apiv1.ProtocolTCP,
					Port:       3000,
					TargetPort: intstr.IntOrString{IntVal: 3000},
				},
			},
			Type: apiv1.ServiceTypeLoadBalancer,
		},
	}
}

func CleanupTestApp(client *kubernetes.KubeClients, name string) error {
	if err := DeleteDeployment(client, name); err != nil {
		return err
	}

	if err := DeleteService(client, name); err != nil {
		return err
	}

	return nil
}

func DeleteDeployment(client *kubernetes.KubeClients, name string) error {
	deploymentsClient := client.ClientSet.AppsV1().Deployments(utils.DaprTestKubeNameSpace)
	deletePolicy := metav1.DeletePropagationForeground
	if err := deploymentsClient.Delete(name, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		return err
	}

	return nil
}

func DeleteService(client *kubernetes.KubeClients, name string) error {
	serviceClient := client.ClientSet.CoreV1().Services(utils.DaprTestKubeNameSpace)

	deletePolicy := metav1.DeletePropagationForeground
	err := serviceClient.Delete(name, &metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})

	if err != nil {
		return err
	}

	return nil
}

func int32Ptr(i int32) *int32 {
	return &i
}
