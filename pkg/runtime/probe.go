package runtime

import (
	"fmt"
	"net"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
)

type PodInfo struct {
	ApplicationProbingPort int
	AppContainer           *v1.Container
	Pod                    *v1.Pod
}

const portScanTimeout = 100 * time.Millisecond

func (rt *DaprRuntime) ProbeApplicationAvailability() (bool, error) {
	if rt.podInfo.Pod != nil && rt.podInfo.AppContainer != nil {
		podStatus := rt.podInfo.Pod.Status

		appContainerState := getContainerStatusByName(&podStatus, rt.podInfo.AppContainer.Name)
		if appContainerState == nil {
			return false, fmt.Errorf("cannot find container %v in pod %v", rt.podInfo.AppContainer.Name, rt.podInfo.Pod.Name)
		} else if appContainerState.Running == nil {
			return false, nil
		}
	}

	if rt.podInfo.ApplicationProbingPort != 0 {
		return scanLocalPort(rt.podInfo.ApplicationProbingPort, portScanTimeout), nil
	} else { // if no container info is found and no port is found, return true by default
		return true, nil
	}
}

func getContainerStatusByName(podStatus *v1.PodStatus, containerName string) *v1.ContainerState {
	for _, status := range podStatus.ContainerStatuses {
		if status.Name == containerName {
			return &status.State
		}
	}
	return nil
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
