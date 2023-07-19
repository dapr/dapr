package injector

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// SplitContainers split containers between:
// - appContainers are containers related to apps.
// - componentContainers are containers related to pluggable components.
func (c *SidecarConfig) SplitContainers() (appContainers map[int]corev1.Container, componentContainers map[int]corev1.Container) {
	appContainers = make(map[int]corev1.Container, len(c.pod.Spec.Containers))
	componentContainers = make(map[int]corev1.Container, len(c.pod.Spec.Containers))
	componentsNames := strings.Split(c.PluggableComponents, ",")
	isComponent := make(map[string]bool, len(componentsNames))
	for _, name := range componentsNames {
		isComponent[name] = true
	}

	for idx, container := range c.pod.Spec.Containers {
		if isComponent[container.Name] {
			componentContainers[idx] = container
		} else {
			appContainers[idx] = container
		}
	}

	return appContainers, componentContainers
}
