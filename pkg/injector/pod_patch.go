package injector

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	log "github.com/Sirupsen/logrus"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

const (
	sidecarContainerName = "actionsrt"
	actionsEnabledKey    = "actions.io/enabled"
	actionsPortKey       = "actions.io/port"
	actionsConfigKey     = "actions.io/config"
	actionsProtocolKey   = "actions.io/protocol"
	actionsIDKey         = "actions.io/id"
	actionsProfilingKey  = "actions.io/profiling"
	actionsLogLevel      = "actions.io/log-level"
	sidecarHTTPPort      = 3500
	sidecarGRPCPORT      = 50001
	apiAddress           = "http://actions-api"
	placementService     = "actions-placement"
	sidecarHTTPPortName  = "actions-http"
	sidecarGRPCPortName  = "actions-grpc"
	defaultLogLevel      = "info"
)

func (i *injector) getPodPatchOperations(ar *v1beta1.AdmissionReview, namespace, image string) ([]PatchOperation, error) {
	req := ar.Request
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		log.Errorf("could not unmarshal raw object: %v", err)
		return nil, err
	}

	log.Infof(
		"AdmissionReview for Kind=%v, Namespace=%v Name=%v (%v) UID=%v "+
			"patchOperation=%v UserInfo=%v",
		req.Kind,
		req.Namespace,
		req.Name,
		pod.Name,
		req.UID,
		req.Operation,
		req.UserInfo,
	)

	if !isResourceActionsEnabled(pod.Annotations) || podContainsSidecarContainer(&pod) {
		return nil, nil
	}

	appPort, err := getAppPort(pod.Annotations)
	if err != nil {
		return nil, err
	}
	config := getConfig(pod.Annotations)
	protocol := getProtocol(pod.Annotations)
	id := getAppID(pod)
	enableProfiling := profilingEnabled(pod.Annotations)
	placementAddress := fmt.Sprintf("%s:80", getKubernetesDNS(placementService, namespace))
	apiSrvAddress := getKubernetesDNS(apiAddress, namespace)
	logLevel := getLogLevel(pod.Annotations)

	appPortStr := ""
	if appPort > 0 {
		appPortStr = fmt.Sprintf("%v", appPort)
	}

	sidecarContainer := getSidecarContainer(appPortStr, protocol, id, config, image, req.Namespace, apiSrvAddress, placementAddress, strconv.FormatBool(enableProfiling), logLevel)

	patchOps := []PatchOperation{}
	var path string
	var value interface{}
	if len(pod.Spec.Containers) == 0 {
		path = "/spec/containers"
		value = []corev1.Container{sidecarContainer}
	} else {
		path = "/spec/containers/-"
		value = sidecarContainer
	}

	patchOps = append(
		patchOps,
		PatchOperation{
			Op:    "add",
			Path:  path,
			Value: value,
		},
	)

	return patchOps, nil
}

func podContainsSidecarContainer(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.Containers {
		if c.Name == sidecarContainerName {
			return true
		}
	}
	return false
}

func getAppPort(annotations map[string]string) (int32, error) {
	p, ok := annotations[actionsPortKey]
	if !ok {
		return -1, nil
	}
	port, err := strconv.ParseInt(p, 10, 32)
	if err != nil {
		return -1, fmt.Errorf("error parsing port int value %s: %s", p, err)
	}
	return int32(port), nil
}

func getConfig(annotations map[string]string) string {
	return annotations[actionsConfigKey]
}

func getProtocol(annotations map[string]string) string {
	if val, ok := annotations[actionsProtocolKey]; ok && val != "" {
		return val
	}
	return "http"
}

func getAppID(pod corev1.Pod) string {
	if val, ok := pod.Annotations[actionsIDKey]; ok && val != "" {
		return val
	}
	return pod.GetName()
}

func getLogLevel(annotations map[string]string) string {
	if val, ok := annotations[actionsLogLevel]; ok && val != "" {
		return val
	}
	return defaultLogLevel
}

func profilingEnabled(annotations map[string]string) bool {
	enabled, ok := annotations[actionsProfilingKey]
	if !ok {
		return false
	}
	switch strings.ToLower(enabled) {
	case "y", "yes", "true", "on", "1":
		return true
	default:
		return false
	}
}

func isResourceActionsEnabled(annotations map[string]string) bool {
	enabled, ok := annotations[actionsEnabledKey]
	if !ok {
		return false
	}
	switch strings.ToLower(enabled) {
	case "y", "yes", "true", "on", "1":
		return true
	default:
		return false
	}
}

func getKubernetesDNS(name, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", name, namespace)
}

func getSidecarContainer(applicationPort, applicationProtocol, id, config, actionSidecarImage, namespace, controlPlaneAddress, placementServiceAddress, enableProfiling, logLevel string) corev1.Container {
	return corev1.Container{
		Name:            sidecarContainerName,
		Image:           actionSidecarImage,
		ImagePullPolicy: corev1.PullAlways,
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: int32(sidecarHTTPPort),
				Name:          sidecarHTTPPortName,
			},
			{
				ContainerPort: int32(sidecarGRPCPORT),
				Name:          sidecarGRPCPortName,
			},
		},
		Command: []string{"/actionsrt"},
		Env:     []corev1.EnvVar{{Name: "HOST_IP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}}}, {Name: "NAMESPACE", Value: namespace}},
		Args:    []string{"--mode", "kubernetes", "--actions-http-port", fmt.Sprintf("%v", sidecarHTTPPort), "--actions-grpc-port", fmt.Sprintf("%v", sidecarGRPCPORT), "--app-port", applicationPort, "--actions-id", id, "--control-plane-address", controlPlaneAddress, "--protocol", applicationProtocol, "--placement-address", placementServiceAddress, "--config", config, "--enable-profiling", enableProfiling, "--log-level", logLevel},
	}
}
