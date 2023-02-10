/*
Copyright 2022 The Dapr Authors
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

package sidecar

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/dapr/dapr/pkg/components/pluggable"
	"github.com/dapr/dapr/pkg/injector/annotations"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	sentryConsts "github.com/dapr/dapr/pkg/sentry/consts"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

// ContainerConfig contains the configuration for the sidecar container.
type ContainerConfig struct {
	AppID                        string
	Annotations                  annotations.Map
	CertChain                    string
	CertKey                      string
	ControlPlaneAddress          string
	DaprSidecarImage             string
	Identity                     string
	IgnoreEntrypointTolerations  []corev1.Toleration
	ImagePullPolicy              corev1.PullPolicy
	MTLSEnabled                  bool
	Namespace                    string
	PlacementServiceAddress      string
	SentryAddress                string
	Tolerations                  []corev1.Toleration
	TrustAnchors                 string
	VolumeMounts                 []corev1.VolumeMount
	ComponentsSocketsVolumeMount *corev1.VolumeMount
	RunAsNonRoot                 bool
	ReadOnlyRootFilesystem       bool
}

var (
	log              = logger.NewLogger("dapr.injector.container")
	probeHTTPHandler = getProbeHTTPHandler(SidecarPublicPort, APIVersionV1, SidecarHealthzPath)
)

// GetSidecarContainer returns the Container object for the sidecar.
func GetSidecarContainer(cfg ContainerConfig) (*corev1.Container, error) {
	appPort, err := cfg.Annotations.GetInt32(annotations.KeyAppPort)
	if err != nil {
		return nil, err
	}
	appPortStr := ""
	if appPort > 0 {
		appPortStr = strconv.Itoa(int(appPort))
	}

	metricsEnabled := cfg.Annotations.GetBoolOrDefault(annotations.KeyEnableMetrics, annotations.DefaultEnableMetric)
	metricsPort := int(cfg.Annotations.GetInt32OrDefault(annotations.KeyMetricsPort, annotations.DefaultMetricsPort))
	sidecarListenAddresses := cfg.Annotations.GetStringOrDefault(annotations.KeySidecarListenAddresses, annotations.DefaultSidecarListenAddresses)
	disableBuiltinK8sSecretStore := cfg.Annotations.GetBoolOrDefault(annotations.KeyDisableBuiltinK8sSecretStore, annotations.DefaultDisableBuiltinK8sSecretStore)

	maxConcurrency, err := cfg.Annotations.GetInt32(annotations.KeyAppMaxConcurrency)
	if err != nil {
		log.Warn(err)
	}

	requestBodySize, err := cfg.Annotations.GetInt32(annotations.KeyHTTPMaxRequestBodySize)
	if err != nil {
		log.Warn(err)
	}

	readBufferSize, err := cfg.Annotations.GetInt32(annotations.KeyHTTPReadBufferSize)
	if err != nil {
		log.Warn(err)
	}

	gracefulShutdownSeconds, err := cfg.Annotations.GetInt32(annotations.KeyGracefulShutdownSeconds)
	if err != nil {
		log.Warn(err)
	}

	if cfg.Annotations.Exist(annotations.KeyPlacementHostAddresses) {
		cfg.PlacementServiceAddress = cfg.Annotations.GetString(annotations.KeyPlacementHostAddresses)
	}

	ports := []corev1.ContainerPort{
		{
			ContainerPort: int32(SidecarHTTPPort),
			Name:          SidecarHTTPPortName,
		},
		{
			ContainerPort: int32(SidecarAPIGRPCPort),
			Name:          SidecarGRPCPortName,
		},
		{
			ContainerPort: int32(SidecarInternalGRPCPort),
			Name:          SidecarInternalGRPCPortName,
		},
		{
			ContainerPort: int32(metricsPort),
			Name:          SidecarMetricsPortName,
		},
	}

	cmd := []string{"/daprd"}

	args := []string{
		"--mode", "kubernetes",
		"--dapr-http-port", strconv.Itoa(SidecarHTTPPort),
		"--dapr-grpc-port", strconv.Itoa(SidecarAPIGRPCPort),
		"--dapr-internal-grpc-port", strconv.Itoa(SidecarInternalGRPCPort),
		"--dapr-listen-addresses", sidecarListenAddresses,
		"--dapr-public-port", strconv.Itoa(SidecarPublicPort),
		"--app-port", appPortStr,
		"--app-id", cfg.AppID,
		"--control-plane-address", cfg.ControlPlaneAddress,
		"--app-protocol", cfg.Annotations.GetStringOrDefault(annotations.KeyAppProtocol, annotations.DefaultAppProtocol),
		"--placement-host-address", cfg.PlacementServiceAddress,
		"--config", cfg.Annotations.GetString(annotations.KeyConfig),
		"--log-level", cfg.Annotations.GetStringOrDefault(annotations.KeyLogLevel, annotations.DefaultLogLevel),
		"--app-max-concurrency", strconv.Itoa(int(maxConcurrency)),
		"--sentry-address", cfg.SentryAddress,
		"--enable-metrics=" + strconv.FormatBool(metricsEnabled),
		"--metrics-port", strconv.Itoa(metricsPort),
		"--dapr-http-max-request-size", strconv.Itoa(int(requestBodySize)),
		"--dapr-http-read-buffer-size", strconv.Itoa(int(readBufferSize)),
		"--dapr-graceful-shutdown-seconds", strconv.Itoa(int(gracefulShutdownSeconds)),
		"--disable-builtin-k8s-secret-store=" + strconv.FormatBool(disableBuiltinK8sSecretStore),
	}

	// --enable-api-logging is set only if there's an explicit annotation (true or false) for that
	// This is because if this CLI flag is missing, the default specified in the Config CRD is used
	if v, ok := cfg.Annotations[annotations.KeyEnableAPILogging]; ok {
		if utils.IsTruthy(v) {
			args = append(args, "--enable-api-logging=true")
		} else {
			args = append(args, "--enable-api-logging=false")
		}
	}

	if cfg.Annotations.GetBoolOrDefault(annotations.KeyEnableAppHealthCheck, annotations.DefaultEnableAppHealthCheck) {
		appHealthCheckPath := cfg.Annotations.GetStringOrDefault(annotations.KeyAppHealthCheckPath, annotations.DefaultAppCheckPath)
		appHealthProbeInterval := cfg.Annotations.GetInt32OrDefault(annotations.KeyAppHealthProbeInterval, annotations.DefaultAppHealthProbeIntervalSeconds)
		appHealthProbeTimeout := cfg.Annotations.GetInt32OrDefault(annotations.KeyAppHealthProbeTimeout, annotations.DefaultAppHealthProbeTimeoutMilliseconds)
		appHealthThreshold := cfg.Annotations.GetInt32OrDefault(annotations.KeyAppHealthThreshold, annotations.DefaultAppHealthThreshold)
		args = append(args,
			"--enable-app-health-check=true",
			"--app-health-check-path", appHealthCheckPath,
			"--app-health-probe-interval", strconv.Itoa(int(appHealthProbeInterval)),
			"--app-health-probe-timeout", strconv.Itoa(int(appHealthProbeTimeout)),
			"--app-health-threshold", strconv.Itoa(int(appHealthThreshold)),
		)
	}

	if cfg.Annotations.GetBoolOrDefault(annotations.KeyEnableDebug, annotations.DefaultEnableDebug) {
		debugPort := cfg.Annotations.GetInt32OrDefault(annotations.KeyDebugPort, annotations.DefaultDebugPort)
		ports = append(ports, corev1.ContainerPort{
			Name:          SidecarDebugPortName,
			ContainerPort: debugPort,
		})

		cmd = []string{"/dlv"}

		args = append([]string{
			fmt.Sprintf("--listen=:%v", debugPort),
			"--accept-multiclient",
			"--headless=true",
			"--log",
			"--api-version=2",
			"exec",
			"/daprd",
			"--",
		}, args...)
	}

	if image := cfg.Annotations.GetString(annotations.KeySidecarImage); image != "" {
		cfg.DaprSidecarImage = image
	}

	container := &corev1.Container{
		Name:            SidecarContainerName,
		Image:           cfg.DaprSidecarImage,
		ImagePullPolicy: cfg.ImagePullPolicy,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.Of(false),
			RunAsNonRoot:             ptr.Of(cfg.RunAsNonRoot),
			ReadOnlyRootFilesystem:   ptr.Of(cfg.ReadOnlyRootFilesystem),
		},
		Ports: ports,
		Args:  append(cmd, args...),
		Env: []corev1.EnvVar{
			{
				Name:  "NAMESPACE",
				Value: cfg.Namespace,
			},
			{
				Name: "POD_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:        probeHTTPHandler,
			InitialDelaySeconds: cfg.Annotations.GetInt32OrDefault(annotations.KeyReadinessProbeDelaySeconds, annotations.DefaultHealthzProbeDelaySeconds),
			TimeoutSeconds:      cfg.Annotations.GetInt32OrDefault(annotations.KeyReadinessProbeTimeoutSeconds, annotations.DefaultHealthzProbeTimeoutSeconds),
			PeriodSeconds:       cfg.Annotations.GetInt32OrDefault(annotations.KeyReadinessProbePeriodSeconds, annotations.DefaultHealthzProbePeriodSeconds),
			FailureThreshold:    cfg.Annotations.GetInt32OrDefault(annotations.KeyReadinessProbeThreshold, annotations.DefaultHealthzProbeThreshold),
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler:        probeHTTPHandler,
			InitialDelaySeconds: cfg.Annotations.GetInt32OrDefault(annotations.KeyLivenessProbeDelaySeconds, annotations.DefaultHealthzProbeDelaySeconds),
			TimeoutSeconds:      cfg.Annotations.GetInt32OrDefault(annotations.KeyLivenessProbeTimeoutSeconds, annotations.DefaultHealthzProbeTimeoutSeconds),
			PeriodSeconds:       cfg.Annotations.GetInt32OrDefault(annotations.KeyLivenessProbePeriodSeconds, annotations.DefaultHealthzProbePeriodSeconds),
			FailureThreshold:    cfg.Annotations.GetInt32OrDefault(annotations.KeyLivenessProbeThreshold, annotations.DefaultHealthzProbeThreshold),
		},
	}

	// If the pod contains any of the tolerations specified by the configuration,
	// the Command and Args are passed as is. Otherwise, the Command is passed as a part of Args.
	// This is to allow the Docker images to specify an ENTRYPOINT
	// which is otherwise overridden by Command.
	if podContainsTolerations(cfg.IgnoreEntrypointTolerations, cfg.Tolerations) {
		container.Command = cmd
		container.Args = args
	} else {
		container.Args = cmd
		container.Args = append(container.Args, args...)
	}

	containerEnv := ParseEnvString(cfg.Annotations[annotations.KeyEnv])
	if len(containerEnv) > 0 {
		container.Env = append(container.Env, containerEnv...)
	}

	// This is a special case that requires administrator privileges in Windows containers
	// to install the certificates to the root store. If this environment variable is set,
	// the container security context should be set to run as administrator.
	for _, env := range container.Env {
		if env.Name == "SSL_CERT_DIR" {
			container.SecurityContext.WindowsOptions = &corev1.WindowsSecurityContextOptions{
				RunAsUserName: ptr.Of("ContainerAdministrator"),
			}

			// We also need to set RunAsNonRoot and ReadOnlyRootFilesystem to false, which would impact Linux too.
			// The injector has no way to know if the pod is going to be deployed on Windows or Linux, so we need to err on the side of most compatibility.
			// On Linux, our containers run with a non-root user, so the net effect shouldn't change: daprd is running as non-root and has no permission to write on the root FS.
			// However certain security scanner may complain about this.
			container.SecurityContext.RunAsNonRoot = ptr.Of(false)
			container.SecurityContext.ReadOnlyRootFilesystem = ptr.Of(false)
			break
		}
	}

	if len(cfg.VolumeMounts) > 0 {
		container.VolumeMounts = append(container.VolumeMounts, cfg.VolumeMounts...)
	}

	if cfg.ComponentsSocketsVolumeMount != nil {
		container.VolumeMounts = append(container.VolumeMounts, *cfg.ComponentsSocketsVolumeMount)
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  pluggable.SocketFolderEnvVar,
			Value: cfg.ComponentsSocketsVolumeMount.MountPath,
		})
	}

	if cfg.Annotations.GetBoolOrDefault(annotations.KeyLogAsJSON, annotations.DefaultLogAsJSON) {
		container.Args = append(container.Args, "--log-as-json")
	}

	if cfg.Annotations.GetBoolOrDefault(annotations.KeyEnableProfiling, annotations.DefaultEnableProfiling) {
		container.Args = append(container.Args, "--enable-profiling")
	}

	container.Env = append(container.Env,
		corev1.EnvVar{
			Name:  sentryConsts.TrustAnchorsEnvVar,
			Value: cfg.TrustAnchors,
		},
		corev1.EnvVar{
			Name:  sentryConsts.CertChainEnvVar,
			Value: cfg.CertChain,
		},
		corev1.EnvVar{
			Name:  sentryConsts.CertKeyEnvVar,
			Value: cfg.CertKey,
		},
		corev1.EnvVar{
			Name:  "SENTRY_LOCAL_IDENTITY",
			Value: cfg.Identity,
		},
	)

	if cfg.MTLSEnabled {
		container.Args = append(container.Args, "--enable-mtls")
	}

	if cfg.Annotations.GetBoolOrDefault(annotations.KeyAppSSL, annotations.DefaultAppSSL) {
		container.Args = append(container.Args, "--app-ssl")
	}

	if secret := cfg.Annotations.GetString(annotations.KeyAPITokenSecret); secret != "" {
		container.Env = append(container.Env, corev1.EnvVar{
			Name: authConsts.APITokenEnvVar,
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

	if appSecret := cfg.Annotations.GetString(annotations.KeyAppTokenSecret); appSecret != "" {
		container.Env = append(container.Env, corev1.EnvVar{
			Name: authConsts.AppAPITokenEnvVar,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "token",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: appSecret,
					},
				},
			},
		})
	}

	resources, err := getResourceRequirements(cfg.Annotations)
	if err != nil {
		log.Warnf("couldn't set container resource requirements: %s. using defaults", err)
	}
	if resources != nil {
		container.Resources = *resources
	}
	return container, nil
}

func getProbeHTTPHandler(port int32, pathElements ...string) corev1.ProbeHandler {
	return corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{
			Path: formatProbePath(pathElements...),
			Port: intstr.IntOrString{IntVal: port},
		},
	}
}

func formatProbePath(elements ...string) string {
	pathStr := path.Join(elements...)
	if !strings.HasPrefix(pathStr, "/") {
		pathStr = "/" + pathStr
	}
	return pathStr
}

// podContainsTolerations returns true if the pod contains any of the tolerations specified in ts.
func podContainsTolerations(ts []corev1.Toleration, podTolerations []corev1.Toleration) bool {
	if len(ts) == 0 || len(podTolerations) == 0 {
		return false
	}

	// If the pod contains any of the tolerations specified, return true.
	for _, t := range ts {
		if utils.Contains(podTolerations, t) {
			return true
		}
	}

	return false
}

func getResourceRequirements(an annotations.Map) (*corev1.ResourceRequirements, error) {
	r := corev1.ResourceRequirements{
		Limits:   corev1.ResourceList{},
		Requests: corev1.ResourceList{},
	}
	cpuLimit, ok := an[annotations.KeyCPULimit]
	if ok {
		list, err := appendQuantityToResourceList(cpuLimit, corev1.ResourceCPU, r.Limits)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar cpu limit: %w", err)
		}
		r.Limits = *list
	}
	memLimit, ok := an[annotations.KeyMemoryLimit]
	if ok {
		list, err := appendQuantityToResourceList(memLimit, corev1.ResourceMemory, r.Limits)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar memory limit: %w", err)
		}
		r.Limits = *list
	}
	cpuRequest, ok := an[annotations.KeyCPURequest]
	if ok {
		list, err := appendQuantityToResourceList(cpuRequest, corev1.ResourceCPU, r.Requests)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar cpu request: %w", err)
		}
		r.Requests = *list
	}
	memRequest, ok := an[annotations.KeyMemoryRequest]
	if ok {
		list, err := appendQuantityToResourceList(memRequest, corev1.ResourceMemory, r.Requests)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar memory request: %w", err)
		}
		r.Requests = *list
	}

	if len(r.Limits) > 0 || len(r.Requests) > 0 {
		return &r, nil
	}
	return nil, nil
}

func appendQuantityToResourceList(quantity string, resourceName corev1.ResourceName, resourceList corev1.ResourceList) (*corev1.ResourceList, error) {
	q, err := resource.ParseQuantity(quantity)
	if err != nil {
		return nil, err
	}
	resourceList[resourceName] = q
	return &resourceList, nil
}
