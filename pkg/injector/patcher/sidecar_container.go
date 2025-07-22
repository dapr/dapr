/*
Copyright 2023 The Dapr Authors
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

package patcher

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/dapr/dapr/pkg/config/protocol"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/ptr"
)

type getSidecarContainerOpts struct {
	VolumeMounts                 []corev1.VolumeMount
	ComponentsSocketsVolumeMount *corev1.VolumeMount
}

// getSidecarContainer returns the Container object for the sidecar.
func (c *SidecarConfig) getSidecarContainer(opts getSidecarContainerOpts) (*corev1.Container, error) {
	// Ports for the daprd container
	ports := []corev1.ContainerPort{
		{
			ContainerPort: c.SidecarHTTPPort,
			Name:          injectorConsts.SidecarHTTPPortName,
		},
		{
			ContainerPort: c.SidecarAPIGRPCPort,
			Name:          injectorConsts.SidecarGRPCPortName,
		},
		{
			ContainerPort: c.SidecarInternalGRPCPort,
			Name:          injectorConsts.SidecarInternalGRPCPortName,
		},
		{
			ContainerPort: c.SidecarMetricsPort,
			Name:          injectorConsts.SidecarMetricsPortName,
		},
	}

	// Get the command (/daprd) and all CLI flags
	cmd := []string{"/daprd"}
	args := []string{
		"--dapr-http-port", strconv.FormatInt(int64(c.SidecarHTTPPort), 10),
		"--dapr-grpc-port", strconv.FormatInt(int64(c.SidecarAPIGRPCPort), 10),
		"--dapr-internal-grpc-port", strconv.FormatInt(int64(c.SidecarInternalGRPCPort), 10),
		"--dapr-listen-addresses", c.SidecarListenAddresses,
		"--dapr-public-port", strconv.FormatInt(int64(c.SidecarPublicPort), 10),
		"--app-id", c.GetAppID(),
		"--app-protocol", c.AppProtocol,
		"--log-level", c.LogLevel,
		"--dapr-graceful-shutdown-seconds", strconv.Itoa(c.GracefulShutdownSeconds),
	}

	// Mode is omitted if it's an unsupported value
	switch c.Mode {
	case injectorConsts.ModeKubernetes:
		args = append(args, "--mode", "kubernetes")
	case injectorConsts.ModeStandalone:
		args = append(args, "--mode", "standalone")
	}

	if c.OperatorAddress != "" {
		args = append(args, "--control-plane-address", c.OperatorAddress)
	}

	if c.SentryAddress != "" {
		args = append(args, "--sentry-address", c.SentryAddress)
	}

	if c.AppPort > 0 {
		args = append(args, "--app-port", strconv.FormatInt(int64(c.AppPort), 10))
	}

	if c.EnableMetrics {
		args = append(args,
			"--enable-metrics",
			"--metrics-port", strconv.FormatInt(int64(c.SidecarMetricsPort), 10),
		)
	}

	if c.Config != "" {
		args = append(args, "--config", c.Config)
	}

	if c.AppChannelAddress != "" {
		args = append(args, "--app-channel-address", c.AppChannelAddress)
	}

	// Actor/placement/reminders services
	// Note that PlacementAddress takes priority over ActorsAddress
	if strings.TrimSpace(c.PlacementAddress) != "" {
		args = append(args, "--placement-host-address", c.PlacementAddress)
	} else if c.ActorsService != "" {
		args = append(args, "--actors-service", c.ActorsService)
	}
	if c.RemindersService != "" {
		args = append(args, "--reminders-service", c.RemindersService)
	}

	if c.SentryRequestJwtAudiences != "" {
		args = append(args, "--sentry-request-jwt-audiences", c.SentryRequestJwtAudiences)
	}

	// --enable-api-logging is set if and only if there's an explicit value (true or false) for that
	// This is set explicitly even if "false"
	// This is because if this CLI flag is missing, the default specified in the Config CRD is used
	if c.EnableAPILogging != nil {
		args = append(args, "--enable-api-logging="+strconv.FormatBool(*c.EnableAPILogging))
	}

	if c.DisableBuiltinK8sSecretStore {
		args = append(args, "--disable-builtin-k8s-secret-store")
	}

	if c.EnableAppHealthCheck {
		args = append(args,
			"--enable-app-health-check",
			"--app-health-check-path", c.AppHealthCheckPath,
			"--app-health-probe-interval", strconv.FormatInt(int64(c.AppHealthProbeInterval), 10),
			"--app-health-probe-timeout", strconv.FormatInt(int64(c.AppHealthProbeTimeout), 10),
			"--app-health-threshold", strconv.FormatInt(int64(c.AppHealthThreshold), 10),
		)
	}

	if c.LogAsJSON {
		args = append(args, "--log-as-json")
	}

	if c.EnableProfiling {
		args = append(args, "--enable-profiling")
	}

	if c.MTLSEnabled {
		args = append(args, "--enable-mtls")
	}

	// Note: we are still passing --app-ssl as-is, rather than merging it into "app-protocol", for backwards-compatibility (ability to inject Dapr 1.10 and older sidecars).
	// We will let Daprd "convert" this.
	if c.AppSSL {
		args = append(args, "--app-ssl")
	}

	if c.AppMaxConcurrency != nil {
		args = append(args, "--app-max-concurrency", strconv.Itoa(*c.AppMaxConcurrency))
	}

	if c.HTTPMaxRequestSize != nil {
		args = append(args, "--dapr-http-max-request-size", strconv.Itoa(*c.HTTPMaxRequestSize))
	}

	if c.MaxBodySize != "" {
		args = append(args, "--max-body-size", c.MaxBodySize)
	}

	if c.HTTPReadBufferSize != nil {
		args = append(args, "--dapr-http-read-buffer-size", strconv.Itoa(*c.HTTPReadBufferSize))
	}

	if c.ReadBufferSize != "" {
		args = append(args, "--read-buffer-size", c.ReadBufferSize)
	}

	if c.UnixDomainSocketPath != "" {
		// Note this is a constant path
		// The passed annotation determines where the socket folder is mounted in the app container, but in the daprd container the path is a constant
		args = append(args, "--unix-domain-socket", injectorConsts.UnixDomainSocketDaprdPath)
	}

	if c.BlockShutdownDuration != nil {
		args = append(args, "--dapr-block-shutdown-duration", *c.BlockShutdownDuration)
	}

	// When debugging is enabled, we need to override the command and the flags
	if c.EnableDebug {
		ports = append(ports, corev1.ContainerPort{
			Name:          injectorConsts.SidecarDebugPortName,
			ContainerPort: c.SidecarDebugPort,
		})

		cmd = []string{"/dlv"}

		args = append([]string{
			"--listen", ":" + strconv.FormatInt(int64(c.SidecarDebugPort), 10),
			"--accept-multiclient",
			"--headless=true",
			"--log",
			"--api-version=2",
			"exec",
			"/daprd",
			"--",
		}, args...)
	}

	// Security context
	securityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.Of(false),
		RunAsNonRoot:             ptr.Of(c.RunAsNonRoot),
		RunAsUser:                c.RunAsUser,
		RunAsGroup:               c.RunAsGroup,
		ReadOnlyRootFilesystem:   ptr.Of(c.ReadOnlyRootFilesystem),
	}
	if c.SidecarSeccompProfileType != "" {
		securityContext.SeccompProfile = &corev1.SeccompProfile{
			Type: corev1.SeccompProfileType(c.SidecarSeccompProfileType),
		}
	}
	if c.SidecarDropALLCapabilities {
		securityContext.Capabilities = &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		}
	}

	// Create the container object
	readinessProbeHandler := getReadinessProbeHandler(c.SidecarPublicPort, injectorConsts.APIVersionV1, injectorConsts.SidecarHealthzPath)
	livenessProbeHandler := getLivenessProbeHandler(c.SidecarPublicPort)
	env := []corev1.EnvVar{
		{
			Name:  "NAMESPACE",
			Value: c.Namespace,
		},
		{
			Name:  securityConsts.TrustAnchorsEnvVar,
			Value: string(c.CurrentTrustAnchors),
		},
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		// TODO: @joshvanl: In v1.14, these two env vars should be moved to flags.
		{
			Name:  securityConsts.ControlPlaneNamespaceEnvVar,
			Value: c.ControlPlaneNamespace,
		},
		{
			Name:  securityConsts.ControlPlaneTrustDomainEnvVar,
			Value: c.ControlPlaneTrustDomain,
		},
	}
	if c.EnableK8sDownwardAPIs {
		env = append(env,
			corev1.EnvVar{
				Name: injectorConsts.DaprContainerHostIP,
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.podIP",
					},
				},
			},
		)
	}

	// Scheduler address could be empty if scheduler service is disabled
	// TODO: remove in v1.16 when daprd no longer needs all scheduler pod
	// addresses for serving.
	if strings.TrimSpace(c.SchedulerAddress) != "" {
		env = append(env,
			corev1.EnvVar{
				Name:  injectorConsts.SchedulerHostAddressEnvVar,
				Value: c.SchedulerAddress,
			},
			corev1.EnvVar{
				Name:  injectorConsts.SchedulerHostAddressDNSAEnvVar,
				Value: c.SchedulerAddressDNSA,
			},
		)
	}

	container := &corev1.Container{
		Name:            injectorConsts.SidecarContainerName,
		Image:           c.SidecarImage,
		ImagePullPolicy: c.ImagePullPolicy,
		SecurityContext: securityContext,
		Ports:           ports,
		Args:            append(cmd, args...),
		Env:             env,
		VolumeMounts:    opts.VolumeMounts,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:        readinessProbeHandler,
			InitialDelaySeconds: c.SidecarReadinessProbeDelaySeconds,
			TimeoutSeconds:      c.SidecarReadinessProbeTimeoutSeconds,
			PeriodSeconds:       c.SidecarReadinessProbePeriodSeconds,
			FailureThreshold:    c.SidecarReadinessProbeThreshold,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler:        livenessProbeHandler,
			InitialDelaySeconds: c.SidecarLivenessProbeDelaySeconds,
			TimeoutSeconds:      c.SidecarLivenessProbeTimeoutSeconds,
			PeriodSeconds:       c.SidecarLivenessProbePeriodSeconds,
			FailureThreshold:    c.SidecarLivenessProbeThreshold,
		},
	}

	// If the pod contains any of the tolerations specified by the configuration,
	// the Command and Args are passed as is. Otherwise, the Command is passed as a part of Args.
	// This is to allow the Docker images to specify an ENTRYPOINT
	// which is otherwise overridden by Command.
	if podContainsTolerations(c.IgnoreEntrypointTolerations, c.pod.Spec.Tolerations) {
		container.Command = cmd
		container.Args = args
	} else {
		container.Args = cmd
		container.Args = append(container.Args, args...)
	}

	// Set env vars if needed
	containerEnvKeys, containerEnv := c.getEnv()
	if len(containerEnv) > 0 {
		container.Env = append(container.Env, containerEnv...)
	}

	containerEnvFromKeys, containerEnvFrom := c.getEnvFromSecret()
	if len(containerEnvFrom) > 0 {
		container.Env = append(container.Env, containerEnvFrom...)
	}

	envKeys := slices.Concat(containerEnvKeys, containerEnvFromKeys)
	if len(envKeys) > 0 {
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  securityConsts.EnvKeysEnvVar,
			Value: strings.Join(envKeys, " "),
		})
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

			// Set RunAsUser and RunAsGroup to default nil to avoid the error when specific user or group is set previously via helm chart.
			container.SecurityContext.RunAsUser = nil
			container.SecurityContext.RunAsGroup = nil
			break
		}
	}

	if opts.ComponentsSocketsVolumeMount != nil {
		container.VolumeMounts = append(container.VolumeMounts, *opts.ComponentsSocketsVolumeMount)
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  injectorConsts.ComponentsUDSMountPathEnvVar,
			Value: opts.ComponentsSocketsVolumeMount.MountPath,
		})
	}

	if c.APITokenSecret != "" {
		container.Env = append(container.Env, corev1.EnvVar{
			Name: securityConsts.APITokenEnvVar,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "token",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: c.APITokenSecret,
					},
				},
			},
		})
	}

	if c.AppTokenSecret != "" {
		container.Env = append(container.Env, corev1.EnvVar{
			Name: securityConsts.AppAPITokenEnvVar,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "token",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: c.AppTokenSecret,
					},
				},
			},
		})
	}

	// Resources for the container
	resources, err := c.getResourceRequirements()
	if err != nil {
		log.Warnf("couldn't set container resource requirements: %s. using defaults", err)
	} else if resources != nil {
		container.Resources = *resources
	}

	return container, nil
}

func (c *SidecarConfig) getResourceRequirements() (*corev1.ResourceRequirements, error) {
	r := corev1.ResourceRequirements{
		Limits:   corev1.ResourceList{},
		Requests: corev1.ResourceList{},
	}
	if c.SidecarCPURequest != "" {
		q, err := resource.ParseQuantity(c.SidecarCPURequest)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar CPU request: %w", err)
		}
		r.Requests[corev1.ResourceCPU] = q
	}
	if c.SidecarCPULimit != "" {
		q, err := resource.ParseQuantity(c.SidecarCPULimit)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar CPU limit: %w", err)
		}
		r.Limits[corev1.ResourceCPU] = q
	}
	if c.SidecarMemoryRequest != "" {
		q, err := resource.ParseQuantity(c.SidecarMemoryRequest)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar memory request: %w", err)
		}
		r.Requests[corev1.ResourceMemory] = q
	}
	if c.SidecarMemoryLimit != "" {
		q, err := resource.ParseQuantity(c.SidecarMemoryLimit)
		if err != nil {
			return nil, fmt.Errorf("error parsing sidecar memory limit: %w", err)
		}
		r.Limits[corev1.ResourceMemory] = q
	}

	if len(r.Limits) == 0 && len(r.Requests) == 0 {
		return nil, nil
	}
	return &r, nil
}

// GetAppID returns the AppID property, fallinb back to the name of the pod.
func (c *SidecarConfig) GetAppID() string {
	if c.AppID == "" {
		log.Warnf("app-id not set defaulting the app-id to: %s", c.pod.GetName())
		return c.pod.GetName()
	}

	return c.AppID
}

var envRegexp = regexp.MustCompile(`(?m)(,)\s*[a-zA-Z\_][a-zA-Z0-9\_]*=`)

// getEnv returns the EnvVar slice from the Env annotation.
func (c *SidecarConfig) getEnv() (envKeys []string, envVars []corev1.EnvVar) {
	return parseEnvVars(c.Env, false)
}

func (c *SidecarConfig) getEnvFromSecret() (envKeys []string, envVars []corev1.EnvVar) {
	return parseEnvVars(c.EnvFromSecret, true)
}

func parseEnvVars(envString string, fromSecret bool) (envKeys []string, envVars []corev1.EnvVar) {
	if envString == "" {
		return []string{}, []corev1.EnvVar{}
	}

	indexes := envRegexp.FindAllStringIndex(envString, -1)
	lastEnd := len(envString)
	parts := make([]string, len(indexes)+1)
	for i := len(indexes) - 1; i >= 0; i-- {
		parts[i+1] = strings.TrimSpace(envString[indexes[i][0]+1 : lastEnd])
		lastEnd = indexes[i][0]
	}
	parts[0] = envString[0:lastEnd]

	envKeys = make([]string, 0, len(parts))
	envVars = make([]corev1.EnvVar, 0, len(parts))

	for _, s := range parts {
		pairs := strings.Split(strings.TrimSpace(s), "=")
		if len(pairs) != 2 {
			continue
		}
		envKey := pairs[0]
		envValue := pairs[1]
		envKeys = append(envKeys, envKey)

		if fromSecret {
			secretSource := createSecretSource(envValue)
			if secretSource != nil {
				envVars = append(envVars, corev1.EnvVar{
					Name:      envKey,
					ValueFrom: secretSource,
				})
			}
		} else {
			envVars = append(envVars, corev1.EnvVar{
				Name:  envKey,
				Value: envValue,
			})
		}
	}

	return envKeys, envVars
}

func createSecretSource(value string) *corev1.EnvVarSource {
	cleanValue := strings.TrimSpace(value)

	if strings.Contains(cleanValue, ":") {
		nameKeyPair := strings.Split(cleanValue, ":")
		if len(nameKeyPair) != 2 {
			return nil
		}
		return &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: strings.TrimSpace(nameKeyPair[0]),
				},
				Key: strings.TrimSpace(nameKeyPair[1]),
			},
		}
	}
	return &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: cleanValue,
			},
			Key: cleanValue,
		},
	}
}

func (c *SidecarConfig) GetAppProtocol() string {
	appProtocol := strings.ToLower(c.AppProtocol)

	switch appProtocol {
	case string(protocol.GRPCSProtocol), string(protocol.HTTPSProtocol), string(protocol.H2CProtocol):
		return appProtocol
	case string(protocol.HTTPProtocol):
		// For backwards compatibility, when protocol is HTTP and --app-ssl is set, use "https"
		// TODO: Remove in a future Dapr version
		if c.AppSSL {
			return string(protocol.HTTPSProtocol)
		} else {
			return string(protocol.HTTPProtocol)
		}
	case string(protocol.GRPCProtocol):
		// For backwards compatibility, when protocol is GRPC and --app-ssl is set, use "grpcs"
		// TODO: Remove in a future Dapr version
		if c.AppSSL {
			return string(protocol.GRPCSProtocol)
		} else {
			return string(protocol.GRPCProtocol)
		}
	case "":
		return string(protocol.HTTPProtocol)
	default:
		return ""
	}
}

func getReadinessProbeHandler(port int32, pathElements ...string) corev1.ProbeHandler {
	return corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{
			Path: formatProbePath(pathElements...),
			Port: intstr.IntOrString{IntVal: port},
		},
	}
}

func getLivenessProbeHandler(port int32) corev1.ProbeHandler {
	return corev1.ProbeHandler{
		TCPSocket: &corev1.TCPSocketAction{
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
