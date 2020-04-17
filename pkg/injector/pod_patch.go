// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package injector

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/dapr/pkg/sentry/certs"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	sidecarContainerName         = "daprd"
	daprEnabledKey               = "dapr.io/enabled"
	daprPortKey                  = "dapr.io/port"
	daprConfigKey                = "dapr.io/config"
	daprProtocolKey              = "dapr.io/protocol"
	appIDKey                     = "dapr.io/id"
	daprProfilingKey             = "dapr.io/profiling"
	daprLogLevel                 = "dapr.io/log-level"
	daprLogAsJSON                = "dapr.io/log-as-json"
	daprMaxConcurrencyKey        = "dapr.io/max-concurrency"
	daprMetricsPortKey           = "dapr.io/metrics-port"
	daprCPULimitKey              = "dapr.io/sidecar-cpu-limit"
	daprMemoryLimitKey           = "dapr.io/sidecar-memory-limit"
	daprCPURequestKey            = "dapr.io/sidecar-cpu-request"
	daprMemoryRequestKey         = "dapr.io/sidecar-memory-request"
	sidecarHTTPPort              = 3500
	sidecarAPIGRPCPort           = 50001
	sidecarInternalGRPCPort      = 50002
	apiAddress                   = "dapr-api"
	placementService             = "dapr-placement"
	sentryService                = "dapr-sentry"
	sidecarHTTPPortName          = "dapr-http"
	sidecarGRPCPortName          = "dapr-grpc"
	sidecarInternalGRPCPortName  = "dapr-internal"
	sidecarMetricsPortName       = "dapr-metrics"
	defaultLogLevel              = "info"
	defaultLogAsJSON             = false
	kubernetesMountPath          = "/var/run/secrets/kubernetes.io/serviceaccount"
	defaultConfig                = "default"
	defaultMetricsPort           = 9090
	sidecarHealthzPath           = "healthz"
	defaultHealthzProbeDelay     = 1
	defaultHealthzProbeTimeout   = 3
	defaultHealthzProbeThreshold = 1
	apiVersionV1                 = "v1.0"
)

func (i *injector) getPodPatchOperations(ar *v1beta1.AdmissionReview,
	namespace, image string, kubeClient *kubernetes.Clientset, daprClient scheme.Interface) ([]PatchOperation, error) {
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

	if !isResourceDaprEnabled(pod.Annotations) || podContainsSidecarContainer(&pod) {
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
	sentryAddress := fmt.Sprintf("%s:80", getKubernetesDNS(sentryService, namespace))
	apiSrvAddress := fmt.Sprintf("%s:80", getKubernetesDNS(apiAddress, namespace))
	logLevel := getLogLevel(pod.Annotations)
	logAsJSON := logAsJSONEnabled(pod.Annotations)
	metricsPort := getMetricsPort(pod.Annotations)
	maxConcurrency, err := getMaxConcurrency(pod.Annotations)
	if err != nil {
		log.Warn(err)
	}

	appPortStr := ""
	if appPort > 0 {
		appPortStr = fmt.Sprintf("%v", appPort)
	}
	maxConcurrencyStr := fmt.Sprintf("%v", maxConcurrency)

	var trustAnchors string
	var certChain string
	var certKey string
	var identity string

	mtlsEnabled := mTLSEnabled(daprClient)
	if mtlsEnabled {
		trustAnchors, certChain, certKey = getTrustAnchorsAndCertChain(kubeClient, namespace)
		identity = fmt.Sprintf("%s:%s", pod.Spec.ServiceAccountName, req.Namespace)
	}

	tokenMount := getTokenVolumeMount(pod)
	resources, err := getResourceRequirements(pod.Annotations)
	if err != nil {
		log.Warnf("couldn't set container resource requirements: %s. using defaults", err)
	}
	sidecarContainer := getSidecarContainer(appPortStr, protocol, id, config, image, req.Namespace, apiSrvAddress, placementAddress, enableProfiling, logLevel, logAsJSON, maxConcurrencyStr, tokenMount, trustAnchors, certChain, certKey, sentryAddress, mtlsEnabled, identity, metricsPort, resources)

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

func getTrustAnchorsAndCertChain(kubeClient *kubernetes.Clientset, namespace string) (string, string, string) {
	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(certs.KubeScrtName, meta_v1.GetOptions{})
	if err != nil {
		return "", "", ""
	}
	rootCert := secret.Data[credentials.RootCertFilename]
	certChain := secret.Data[credentials.IssuerCertFilename]
	certKey := secret.Data[credentials.IssuerKeyFilename]
	return string(rootCert), string(certChain), string(certKey)
}

func mTLSEnabled(daprClient scheme.Interface) bool {
	resp, err := daprClient.ConfigurationV1alpha1().Configurations(meta_v1.NamespaceAll).List(meta_v1.ListOptions{})
	if err != nil {
		// mTLS enabled by default
		return true
	}

	for _, c := range resp.Items {
		if c.GetName() == defaultConfig {
			return c.Spec.MTLSSpec.Enabled
		}
	}
	return true
}

func getTokenVolumeMount(pod corev1.Pod) *corev1.VolumeMount {
	for _, c := range pod.Spec.Containers {
		for _, v := range c.VolumeMounts {
			if v.MountPath == kubernetesMountPath {
				return &v
			}
		}
	}
	return nil
}

func podContainsSidecarContainer(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.Containers {
		if c.Name == sidecarContainerName {
			return true
		}
	}
	return false
}

func getMaxConcurrency(annotations map[string]string) (int32, error) {
	m, ok := annotations[daprMaxConcurrencyKey]
	if !ok {
		return -1, nil
	}
	maxConcurrency, err := strconv.ParseInt(m, 10, 32)
	if err != nil {
		return -1, fmt.Errorf("error parsing max concurrency int value %s: %s", m, err)
	}
	return int32(maxConcurrency), nil
}

func getAppPort(annotations map[string]string) (int32, error) {
	p, ok := annotations[daprPortKey]
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
	return annotations[daprConfigKey]
}

func getProtocol(annotations map[string]string) string {
	if val, ok := annotations[daprProtocolKey]; ok && val != "" {
		return val
	}
	return "http"
}

func getMetricsPort(annotations map[string]string) int {
	if val, ok := annotations[daprMetricsPortKey]; ok && val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			return v
		}
	}
	return defaultMetricsPort
}

func getAppID(pod corev1.Pod) string {
	if val, ok := pod.Annotations[appIDKey]; ok && val != "" {
		return val
	}
	return pod.GetName()
}

func getLogLevel(annotations map[string]string) string {
	if val, ok := annotations[daprLogLevel]; ok && val != "" {
		return val
	}
	return defaultLogLevel
}

func logAsJSONEnabled(annotations map[string]string) bool {
	enabled, ok := annotations[daprLogAsJSON]
	if !ok {
		return defaultLogAsJSON
	}
	if strings.EqualFold(enabled, "true") {
		return true
	}
	return false
}

func profilingEnabled(annotations map[string]string) bool {
	enabled, ok := annotations[daprProfilingKey]
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

func appendQuantityToResourceList(quantity string, resourceName v1.ResourceName, resourceList v1.ResourceList) (*v1.ResourceList, error) {
	q, err := resource.ParseQuantity(quantity)
	if err != nil {
		return nil, err
	}
	resourceList[resourceName] = q
	return &resourceList, nil
}

func getResourceRequirements(annotations map[string]string) (*v1.ResourceRequirements, error) {
	r := v1.ResourceRequirements{
		Limits:   v1.ResourceList{},
		Requests: v1.ResourceList{},
	}
	cpuLimit, ok := annotations[daprCPULimitKey]
	if ok {
		list, err := appendQuantityToResourceList(cpuLimit, v1.ResourceCPU, r.Limits)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar cpu limit: %s", err)
		}
		r.Limits = *list
	}
	memLimit, ok := annotations[daprMemoryLimitKey]
	if ok {
		list, err := appendQuantityToResourceList(memLimit, v1.ResourceMemory, r.Limits)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar memory limit: %s", err)
		}
		r.Limits = *list
	}
	cpuRequest, ok := annotations[daprCPURequestKey]
	if ok {
		list, err := appendQuantityToResourceList(cpuRequest, v1.ResourceCPU, r.Requests)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar cpu request: %s", err)
		}
		r.Requests = *list
	}
	memRequest, ok := annotations[daprMemoryRequestKey]
	if ok {
		list, err := appendQuantityToResourceList(memRequest, v1.ResourceMemory, r.Requests)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar memory request: %s", err)
		}
		r.Requests = *list
	}

	if len(r.Limits) > 0 || len(r.Requests) > 0 {
		return &r, nil
	}
	return nil, nil
}

func isResourceDaprEnabled(annotations map[string]string) bool {
	enabled, ok := annotations[daprEnabledKey]
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

func getSidecarContainer(applicationPort, applicationProtocol, id, config, daprSidecarImage, namespace, controlPlaneAddress, placementServiceAddress string, enableProfiling bool, logLevel string, logAsJSON bool, maxConcurrency string, tokenVolumeMount *corev1.VolumeMount, trustAnchors, certChain, certKey, sentryAddress string, mtlsEnabled bool, identity string, metricsPort int, resources *v1.ResourceRequirements) corev1.Container {
	c := corev1.Container{
		Name:            sidecarContainerName,
		Image:           daprSidecarImage,
		ImagePullPolicy: corev1.PullAlways,
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: int32(sidecarHTTPPort),
				Name:          sidecarHTTPPortName,
			},
			{
				ContainerPort: int32(sidecarAPIGRPCPort),
				Name:          sidecarGRPCPortName,
			},
			{
				ContainerPort: int32(sidecarInternalGRPCPort),
				Name:          sidecarInternalGRPCPortName,
			},
			{
				ContainerPort: int32(metricsPort),
				Name:          sidecarMetricsPortName,
			},
		},
		Command: []string{"/daprd"},
		Env: []corev1.EnvVar{
			{
				Name: runtime.HostIPEnvVar,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.podIP",
					},
				},
			},
			{
				Name:  "NAMESPACE",
				Value: namespace,
			},
		},
		Args: []string{
			"--mode", "kubernetes",
			"--dapr-http-port", fmt.Sprintf("%v", sidecarHTTPPort),
			"--dapr-grpc-port", fmt.Sprintf("%v", sidecarAPIGRPCPort),
			"--dapr-internal-grpc-port", fmt.Sprintf("%v", sidecarInternalGRPCPort),
			"--app-port", applicationPort,
			"--app-id", id,
			"--control-plane-address", controlPlaneAddress,
			"--protocol", applicationProtocol,
			"--placement-address", placementServiceAddress,
			"--config", config,
			"--log-level", logLevel,
			"--max-concurrency", maxConcurrency,
			"--sentry-address", sentryAddress,
			"--metrics-port", fmt.Sprintf("%v", metricsPort),
		},
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: fmt.Sprintf("%s/%s", apiVersionV1, sidecarHealthzPath),
					Port: intstr.IntOrString{IntVal: sidecarHTTPPort},
				},
			},
			InitialDelaySeconds: defaultHealthzProbeDelay,
			TimeoutSeconds:      defaultHealthzProbeTimeout,
			PeriodSeconds:       2 * defaultHealthzProbeTimeout,
			FailureThreshold:    3 * defaultHealthzProbeThreshold,
		},
		LivenessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: fmt.Sprintf("%s/%s", apiVersionV1, sidecarHealthzPath),
					Port: intstr.IntOrString{IntVal: sidecarHTTPPort},
				},
			},
			InitialDelaySeconds: 3 * defaultHealthzProbeDelay,
			TimeoutSeconds:      defaultHealthzProbeTimeout,
			PeriodSeconds:       2 * defaultHealthzProbeTimeout,
			FailureThreshold:    3 * defaultHealthzProbeThreshold,
		},
	}

	if tokenVolumeMount != nil {
		c.VolumeMounts = []corev1.VolumeMount{
			*tokenVolumeMount,
		}
	}

	if logAsJSON {
		c.Args = append(c.Args, "--log-as-json")
	}

	if enableProfiling {
		c.Args = append(c.Args, "--enable-profiling")
	}

	if mtlsEnabled && trustAnchors != "" {
		c.Args = append(c.Args, "--enable-mtls")
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  certs.TrustAnchorsEnvVar,
			Value: trustAnchors,
		},
			corev1.EnvVar{
				Name:  certs.CertChainEnvVar,
				Value: certChain,
			},
			corev1.EnvVar{
				Name:  certs.CertKeyEnvVar,
				Value: certKey,
			},
			corev1.EnvVar{
				Name:  "SENTRY_LOCAL_IDENTITY",
				Value: identity,
			})
	}
	if resources != nil {
		c.Resources = *resources
	}
	return c
}
