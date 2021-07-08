package runtime

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type PodInfo struct {
	ApplicationProbingPort int
	podName                string
}

const portScanTimeout = 100 * time.Millisecond

func (rt *DaprRuntime) ProbeApplicationAvailability() (bool, error) {
	pod, err := getPod(rt.namespace, rt.podInfo.podName)
	if err != nil {
		return false, err
	}

	if pod != nil {
		podStatus := pod.Status

		appContainerName := getAppContainer(pod).Name
		appContainerStatus := getContainerStatusByName(&podStatus, appContainerName)
		if appContainerName == "" {
			return false, fmt.Errorf("cannot identify app container in pod %v", pod.Name)
		} else if !appContainerStatus.Ready {
			log.Debugf("app container status: %+v", appContainerStatus)
			return false, nil
		}
	}

	if rt.podInfo.ApplicationProbingPort != 0 {
		return scanLocalPort(rt.podInfo.ApplicationProbingPort, portScanTimeout), nil
	} else { // if no container info is found and no port is found, return true by default
		return true, nil
	}
}

func getPod(namespace string, podName string) (*v1.Pod, error) {
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("defining k8s config: %v", err.Error())
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("getting k8s kubeClient: %v", err.Error())
	}

	pod, err := kubeClient.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("fetching pod metadata: %v", err.Error())
	}

	return pod, nil
}

func getAppContainer(pod *v1.Pod) v1.Container {
	var appContainer v1.Container
	containers := pod.Spec.Containers
	if len(containers) == 2 {
		for _, container := range containers {
			log.Debugf("container name: %v", container.Name)
			if container.Name != sidecarContainerName {
				log.Debugf("container name selected: %v", container.Name)
				appContainer = container
				break
			}
		}
	}
	return appContainer
}

func getContainerStatusByName(podStatus *v1.PodStatus, containerName string) v1.ContainerStatus {
	for _, status := range podStatus.ContainerStatuses {
		if status.Name == containerName {
			return status
		}
	}
	return v1.ContainerStatus{}
}

func scanLocalPort(port int, timeout time.Duration) bool {
	target := fmt.Sprintf("%s:%d", "127.0.0.1", port)
	conn, err := net.DialTimeout("tcp", target, timeout)

	if err != nil {
		if strings.Contains(err.Error(), "too many open files") {
			time.Sleep(timeout)
			return scanLocalPort(port, timeout)
		}
		return false
	}

	conn.Close()
	return true
}
