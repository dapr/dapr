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
	auth "github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/sentry/certs"
	"github.com/dapr/dapr/utils"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	sidecarContainerName              = "daprd"
	daprEnabledKey                    = "dapr.io/enabled"
	daprPortKey                       = "dapr.io/port"
	daprConfigKey                     = "dapr.io/config"
	daprProtocolKey                   = "dapr.io/protocol"
	appIDKey                          = "dapr.io/id"
	daprProfilingKey                  = "dapr.io/profiling"
	daprLogLevel                      = "dapr.io/log-level"
	daprAPITokenSecret                = "dapr.io/api-token-secret" /* #nosec */
	daprLogAsJSON                     = "dapr.io/log-as-json"
	daprMaxConcurrencyKey             = "dapr.io/max-concurrency"
	daprMetricsPortKey                = "dapr.io/metrics-port"
	daprCPULimitKey                   = "dapr.io/sidecar-cpu-limit"
	daprMemoryLimitKey                = "dapr.io/sidecar-memory-limit"
	daprCPURequestKey                 = "dapr.io/sidecar-cpu-request"
	daprMemoryRequestKey              = "dapr.io/sidecar-memory-request"
	daprLivenessProbeDelayKey         = "dapr.io/sidecar-liveness-probe-delay-seconds"
	daprLivenessProbeTimeoutKey       = "dapr.io/sidecar-liveness-probe-timeout-seconds"
	daprLivenessProbePeriodKey        = "dapr.io/sidecar-liveness-probe-period-seconds"
	daprLivenessProbeThresholdKey     = "dapr.io/sidecar-liveness-probe-threshold"
	daprReadinessProbeDelayKey        = "dapr.io/sidecar-readiness-probe-delay-seconds"
	daprReadinessProbeTimeoutKey      = "dapr.io/sidecar-readiness-probe-timeout-seconds"
	daprReadinessProbePeriodKey       = "dapr.io/sidecar-readiness-probe-period-seconds"
	daprReadinessProbeThresholdKey    = "dapr.io/sidecar-readiness-probe-threshold"
	sidecarHTTPPort                   = 3500
	sidecarAPIGRPCPort                = 50001
	sidecarInternalGRPCPort           = 50002
	apiAddress                        = "dapr-api"
	placementService                  = "dapr-placement"
	sentryService                     = "dapr-sentry"
	sidecarHTTPPortName               = "dapr-http"
	sidecarGRPCPortName               = "dapr-grpc"
	sidecarInternalGRPCPortName       = "dapr-internal"
	sidecarMetricsPortName            = "dapr-metrics"
	defaultLogLevel                   = "info"
	defaultLogAsJSON                  = false
	kubernetesMountPath               = "/var/run/secrets/kubernetes.io/serviceaccount"
	defaultConfig                     = "default"
	defaultMetricsPort                = 9090
	sidecarHealthzPath                = "healthz"
	defaultHealthzProbeDelaySeconds   = 3
	defaultHealthzProbeTimeoutSeconds = 3
	defaultHealthzProbePeriodSeconds  = 6
	defaultHealthzProbeThreshold      = 3
	apiVersionV1                      = "v1.0"
	defaultMtlsEnabled                = true
	trueString                        = "true"
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

	id := getAppID(pod)
	// Keep DNS resolution outside of getSidecarContainer for unit testing.
	placementAddress := fmt.Sprintf("%s:80", getKubernetesDNS(placementService, namespace))
	sentryAddress := fmt.Sprintf("%s:80", getKubernetesDNS(sentryService, namespace))
	apiSrvAddress := fmt.Sprintf("%s:80", getKubernetesDNS(apiAddress, namespace))

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
	sidecarContainer, err := getSidecarContainer(pod.Annotations, id, image, req.Namespace, apiSrvAddress, placementAddress, tokenMount, trustAnchors, certChain, certKey, sentryAddress, mtlsEnabled, identity)
	if err != nil {
		return nil, err
	}

	patchOps := []PatchOperation{}
	var path string
	var value interface{}
	if len(pod.Spec.Containers) == 0 {
		path = "/spec/containers"
		value = []corev1.Container{*sidecarContainer}
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
		return defaultMtlsEnabled
	}

	for _, c := range resp.Items {
		if c.GetName() == defaultConfig {
			return c.Spec.MTLSSpec.Enabled
		}
	}
	return defaultMtlsEnabled
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
	return getInt32Annotation(annotations, daprMaxConcurrencyKey)
}

func getAppPort(annotations map[string]string) (int32, error) {
	return getInt32Annotation(annotations, daprPortKey)
}

func getConfig(annotations map[string]string) string {
	return getStringAnnotation(annotations, daprConfigKey)
}

func getProtocol(annotations map[string]string) string {
	return getStringAnnotationOrDefault(annotations, daprProtocolKey, "http")
}

func getMetricsPort(annotations map[string]string) int {
	return int(getInt32AnnotationOrDefault(annotations, daprMetricsPortKey, defaultMetricsPort))
}

func getAppID(pod corev1.Pod) string {
	return getStringAnnotationOrDefault(pod.Annotations, appIDKey, pod.GetName())
}

func getLogLevel(annotations map[string]string) string {
	return getStringAnnotationOrDefault(annotations, daprLogLevel, defaultLogLevel)
}

func logAsJSONEnabled(annotations map[string]string) bool {
	return getBoolAnnotationOrDefault(annotations, daprLogAsJSON, defaultLogAsJSON)
}

func profilingEnabled(annotations map[string]string) bool {
	return getBoolAnnotationOrDefault(annotations, daprProfilingKey, false)
}

func getAPITokenSecret(annotations map[string]string) string {
	return getStringAnnotationOrDefault(annotations, daprAPITokenSecret, "")
}

func getBoolAnnotationOrDefault(annotations map[string]string, key string, defaultValue bool) bool {
	enabled, ok := annotations[key]
	if !ok {
		return defaultValue
	}
	s := strings.ToLower(enabled)
	// trueString is used to silence a lint error.
	return (s == "y") || (s == "yes") || (s == trueString) || (s == "on") || (s == "1")
}

func getStringAnnotationOrDefault(annotations map[string]string, key, defaultValue string) string {
	if val, ok := annotations[key]; ok && val != "" {
		return val
	}
	return defaultValue
}

func getStringAnnotation(annotations map[string]string, key string) string {
	return annotations[key]
}

func getInt32AnnotationOrDefault(annotations map[string]string, key string, defaultValue int) int32 {
	s, ok := annotations[key]
	if !ok {
		return int32(defaultValue)
	}
	value, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return int32(defaultValue)
	}
	return int32(value)
}

func getInt32Annotation(annotations map[string]string, key string) (int32, error) {
	s, ok := annotations[key]
	if !ok {
		return -1, nil
	}
	value, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return -1, fmt.Errorf("error parsing %s int value %s: %s", key, s, err)
	}
	return int32(value), nil
}

func appendQuantityToResourceList(quantity string, resourceName corev1.ResourceName, resourceList corev1.ResourceList) (*corev1.ResourceList, error) {
	q, err := resource.ParseQuantity(quantity)
	if err != nil {
		return nil, err
	}
	resourceList[resourceName] = q
	return &resourceList, nil
}

func getResourceRequirements(annotations map[string]string) (*corev1.ResourceRequirements, error) {
	r := corev1.ResourceRequirements{
		Limits:   corev1.ResourceList{},
		Requests: corev1.ResourceList{},
	}
	cpuLimit, ok := annotations[daprCPULimitKey]
	if ok {
		list, err := appendQuantityToResourceList(cpuLimit, corev1.ResourceCPU, r.Limits)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar cpu limit: %s", err)
		}
		r.Limits = *list
	}
	memLimit, ok := annotations[daprMemoryLimitKey]
	if ok {
		list, err := appendQuantityToResourceList(memLimit, corev1.ResourceMemory, r.Limits)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar memory limit: %s", err)
		}
		r.Limits = *list
	}
	cpuRequest, ok := annotations[daprCPURequestKey]
	if ok {
		list, err := appendQuantityToResourceList(cpuRequest, corev1.ResourceCPU, r.Requests)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar cpu request: %s", err)
		}
		r.Requests = *list
	}
	memRequest, ok := annotations[daprMemoryRequestKey]
	if ok {
		list, err := appendQuantityToResourceList(memRequest, corev1.ResourceMemory, r.Requests)
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
	return getBoolAnnotationOrDefault(annotations, daprEnabledKey, false)
}

func getKubernetesDNS(name, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", name, namespace)
}

func getSidecarContainer(annotations map[string]string, id, daprSidecarImage, namespace, controlPlaneAddress, placementServiceAddress string, tokenVolumeMount *corev1.VolumeMount, trustAnchors, certChain, certKey, sentryAddress string, mtlsEnabled bool, identity string) (*corev1.Container, error) {
	appPort, err := getAppPort(annotations)
	if err != nil {
		return nil, err
	}
	appPortStr := ""
	if appPort > 0 {
		appPortStr = fmt.Sprintf("%v", appPort)
	}

	metricsPort := getMetricsPort(annotations)
	maxConcurrency, err := getMaxConcurrency(annotations)
	if err != nil {
		log.Warn(err)
	}

	c := &corev1.Container{
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
				Name: utils.HostIPEnvVar,
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
			"--app-port", appPortStr,
			"--app-id", id,
			"--control-plane-address", controlPlaneAddress,
			"--protocol", getProtocol(annotations),
			"--placement-address", placementServiceAddress,
			"--config", getConfig(annotations),
			"--log-level", getLogLevel(annotations),
			"--max-concurrency", fmt.Sprintf("%v", maxConcurrency),
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
			InitialDelaySeconds: getInt32AnnotationOrDefault(annotations, daprReadinessProbeDelayKey, defaultHealthzProbeDelaySeconds),
			TimeoutSeconds:      getInt32AnnotationOrDefault(annotations, daprReadinessProbeTimeoutKey, defaultHealthzProbeTimeoutSeconds),
			PeriodSeconds:       getInt32AnnotationOrDefault(annotations, daprReadinessProbePeriodKey, defaultHealthzProbePeriodSeconds),
			FailureThreshold:    getInt32AnnotationOrDefault(annotations, daprReadinessProbeThresholdKey, defaultHealthzProbeThreshold),
		},
		LivenessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: fmt.Sprintf("%s/%s", apiVersionV1, sidecarHealthzPath),
					Port: intstr.IntOrString{IntVal: sidecarHTTPPort},
				},
			},
			InitialDelaySeconds: getInt32AnnotationOrDefault(annotations, daprLivenessProbeDelayKey, defaultHealthzProbeDelaySeconds),
			TimeoutSeconds:      getInt32AnnotationOrDefault(annotations, daprLivenessProbeTimeoutKey, defaultHealthzProbeTimeoutSeconds),
			PeriodSeconds:       getInt32AnnotationOrDefault(annotations, daprLivenessProbePeriodKey, defaultHealthzProbePeriodSeconds),
			FailureThreshold:    getInt32AnnotationOrDefault(annotations, daprLivenessProbeThresholdKey, defaultHealthzProbeThreshold),
		},
	}

	if tokenVolumeMount != nil {
		c.VolumeMounts = []corev1.VolumeMount{
			*tokenVolumeMount,
		}
	}

	if logAsJSONEnabled(annotations) {
		c.Args = append(c.Args, "--log-as-json")
	}

	if profilingEnabled(annotations) {
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

	secret := getAPITokenSecret(annotations)
	if secret != "" {
		c.Env = append(c.Env, corev1.EnvVar{
			Name: auth.APITokenEnvVar,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "token",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret,
					},
				},
			},
		})
	}

	resources, err := getResourceRequirements(annotations)
	if err != nil {
		log.Warnf("couldn't set container resource requirements: %s. using defaults", err)
	}
	if resources != nil {
		c.Resources = *resources
	}
	return c, nil
}
