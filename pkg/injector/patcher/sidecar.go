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
	"reflect"
	"strconv"
	"strings"

	"github.com/spf13/cast"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/pkg/injector/annotations"
	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	kitstrings "github.com/dapr/kit/strings"
)

// GetInjectedComponentContainersFn is a function that returns the list of component containers for a given appID and namespace.
type GetInjectedComponentContainersFn = func(appID string, namespace string) ([]corev1.Container, error)

// SidecarConfig contains the configuration for the sidecar container.
// Its parameters can be read from annotations on a pod.
// Note: make sure that the annotations defined here are in-sync with the constants in the pkg/injector/annotations package.
type SidecarConfig struct {
	GetInjectedComponentContainers GetInjectedComponentContainersFn

	Mode                        injectorConsts.DaprMode `default:"kubernetes"`
	Namespace                   string
	MTLSEnabled                 bool
	Identity                    string
	IgnoreEntrypointTolerations []corev1.Toleration
	ImagePullPolicy             corev1.PullPolicy
	OperatorAddress             string
	SentryAddress               string
	RunAsNonRoot                bool
	RunAsUser                   *int64
	RunAsGroup                  *int64
	EnableK8sDownwardAPIs       bool
	ReadOnlyRootFilesystem      bool
	SidecarDropALLCapabilities  bool
	DisableTokenVolume          bool
	CurrentTrustAnchors         []byte
	ControlPlaneNamespace       string
	ControlPlaneTrustDomain     string
	ActorsService               string
	RemindersService            string
	SentrySPIFFEID              string
	SidecarHTTPPort             int32 `default:"3500"`
	SidecarPublicPort           int32 `default:"3501"`
	SchedulerAddressDNSA        string

	Enabled                             bool    `annotation:"dapr.io/enabled"`
	AppPort                             int32   `annotation:"dapr.io/app-port"`
	Config                              string  `annotation:"dapr.io/config"`
	AppProtocol                         string  `annotation:"dapr.io/app-protocol" default:"http"`
	AppSSL                              bool    `annotation:"dapr.io/app-ssl"` // TODO: Deprecated in Dapr 1.11; remove in a future Dapr version
	AppID                               string  `annotation:"dapr.io/app-id"`
	EnableProfiling                     bool    `annotation:"dapr.io/enable-profiling"`
	LogLevel                            string  `annotation:"dapr.io/log-level" default:"info"`
	APITokenSecret                      string  `annotation:"dapr.io/api-token-secret"`
	AppTokenSecret                      string  `annotation:"dapr.io/app-token-secret"`
	LogAsJSON                           bool    `annotation:"dapr.io/log-as-json"`
	AppMaxConcurrency                   *int    `annotation:"dapr.io/app-max-concurrency"`
	EnableMetrics                       bool    `annotation:"dapr.io/enable-metrics" default:"true"`
	SidecarMetricsPort                  int32   `annotation:"dapr.io/metrics-port" default:"9090"`
	EnableDebug                         bool    `annotation:"dapr.io/enable-debug" default:"false"`
	SidecarDebugPort                    int32   `annotation:"dapr.io/debug-port" default:"40000"`
	Env                                 string  `annotation:"dapr.io/env"`
	EnvFromSecret                       string  `annotation:"dapr.io/env-from-secret"`
	SidecarAPIGRPCPort                  int32   `annotation:"dapr.io/grpc-port" default:"50001"`
	SidecarInternalGRPCPort             int32   `annotation:"dapr.io/internal-grpc-port" default:"50002"`
	SidecarCPURequest                   string  `annotation:"dapr.io/sidecar-cpu-request"`
	SidecarCPULimit                     string  `annotation:"dapr.io/sidecar-cpu-limit"`
	SidecarMemoryRequest                string  `annotation:"dapr.io/sidecar-memory-request"`
	SidecarMemoryLimit                  string  `annotation:"dapr.io/sidecar-memory-limit"`
	SidecarListenAddresses              string  `annotation:"dapr.io/sidecar-listen-addresses" default:"[::1],127.0.0.1"`
	SidecarLivenessProbeDelaySeconds    int32   `annotation:"dapr.io/sidecar-liveness-probe-delay-seconds"    default:"3"`
	SidecarLivenessProbeTimeoutSeconds  int32   `annotation:"dapr.io/sidecar-liveness-probe-timeout-seconds"  default:"3"`
	SidecarLivenessProbePeriodSeconds   int32   `annotation:"dapr.io/sidecar-liveness-probe-period-seconds"   default:"6"`
	SidecarLivenessProbeThreshold       int32   `annotation:"dapr.io/sidecar-liveness-probe-threshold"        default:"3"`
	SidecarReadinessProbeDelaySeconds   int32   `annotation:"dapr.io/sidecar-readiness-probe-delay-seconds"   default:"3"`
	SidecarReadinessProbeTimeoutSeconds int32   `annotation:"dapr.io/sidecar-readiness-probe-timeout-seconds" default:"3"`
	SidecarReadinessProbePeriodSeconds  int32   `annotation:"dapr.io/sidecar-readiness-probe-period-seconds"  default:"6"`
	SidecarReadinessProbeThreshold      int32   `annotation:"dapr.io/sidecar-readiness-probe-threshold"       default:"3"`
	SidecarImage                        string  `annotation:"dapr.io/sidecar-image"`
	SidecarSeccompProfileType           string  `annotation:"dapr.io/sidecar-seccomp-profile-type"`
	HTTPMaxRequestSize                  *int    `annotation:"dapr.io/http-max-request-size"` // Legacy flag
	MaxBodySize                         string  `annotation:"dapr.io/max-body-size"`
	HTTPReadBufferSize                  *int    `annotation:"dapr.io/http-read-buffer-size"` // Legacy flag
	ReadBufferSize                      string  `annotation:"dapr.io/read-buffer-size"`
	GracefulShutdownSeconds             int     `annotation:"dapr.io/graceful-shutdown-seconds"               default:"-1"`
	BlockShutdownDuration               *string `annotation:"dapr.io/block-shutdown-duration"`
	EnableAPILogging                    *bool   `annotation:"dapr.io/enable-api-logging"`
	UnixDomainSocketPath                string  `annotation:"dapr.io/unix-domain-socket-path"`
	VolumeMounts                        string  `annotation:"dapr.io/volume-mounts"`
	VolumeMountsRW                      string  `annotation:"dapr.io/volume-mounts-rw"`
	DisableBuiltinK8sSecretStore        bool    `annotation:"dapr.io/disable-builtin-k8s-secret-store"`
	EnableAppHealthCheck                bool    `annotation:"dapr.io/enable-app-health-check"`
	AppHealthCheckPath                  string  `annotation:"dapr.io/app-health-check-path" default:"/healthz"`
	AppHealthProbeInterval              int32   `annotation:"dapr.io/app-health-probe-interval" default:"5"`  // In seconds
	AppHealthProbeTimeout               int32   `annotation:"dapr.io/app-health-probe-timeout" default:"500"` // In milliseconds
	AppHealthThreshold                  int32   `annotation:"dapr.io/app-health-threshold" default:"3"`
	PlacementAddress                    string  `annotation:"dapr.io/placement-host-address"`
	SchedulerAddress                    string  `annotation:"dapr.io/scheduler-host-address"`
	PluggableComponents                 string  `annotation:"dapr.io/pluggable-components"`
	PluggableComponentsSocketsFolder    string  `annotation:"dapr.io/pluggable-components-sockets-folder"`
	ComponentContainer                  string  `annotation:"dapr.io/component-container"`
	InjectPluggableComponents           bool    `annotation:"dapr.io/inject-pluggable-components"`
	AppChannelAddress                   string  `annotation:"dapr.io/app-channel-address"`
	SentryRequestJwtAudiences           string  `annotation:"dapr.io/sentry-request-jwt-audiences"`

	pod *corev1.Pod
}

// NewSidecarConfig returns a ContainerConfig object for a pod.
func NewSidecarConfig(pod *corev1.Pod) *SidecarConfig {
	c := &SidecarConfig{
		pod: pod,
	}
	c.setDefaultValues()
	return c
}

func (c *SidecarConfig) SetFromPodAnnotations() {
	c.setFromAnnotations(c.pod.Annotations)
}

func (c *SidecarConfig) setDefaultValues() {
	// Iterate through the fields using reflection
	val := reflect.ValueOf(c).Elem()
	for i := range val.NumField() {
		fieldT := val.Type().Field(i)
		fieldV := val.Field(i)
		def := fieldT.Tag.Get("default")
		if !fieldV.CanSet() || def == "" {
			continue
		}

		// Assign the default value
		setValueFromString(fieldT.Type, fieldV, def, "")
	}
}

// setFromAnnotations updates the object with properties from an annotation map.
func (c *SidecarConfig) setFromAnnotations(an map[string]string) {
	// Iterate through the fields using reflection
	val := reflect.ValueOf(c).Elem()
	for i := range val.NumField() {
		fieldV := val.Field(i)
		fieldT := val.Type().Field(i)
		key := fieldT.Tag.Get("annotation")
		if !fieldV.CanSet() || key == "" {
			continue
		}

		// Skip annotations that are not defined, respect user defined "" for fields to disable them
		if _, exists := an[key]; !exists {
			continue
		}

		// Special cleanup for placement and scheduler addresses defined by annotations being empty
		if key == annotations.KeyPlacementHostAddresses || key == annotations.KeySchedulerHostAddresses {
			trimmed := strings.TrimSpace(strings.Trim(an[key], `"'`))
			an[key] = trimmed
		}

		// Assign the value
		setValueFromString(fieldT.Type, fieldV, an[key], key)
	}
}

func setValueFromString(rt reflect.Type, rv reflect.Value, val string, key string) bool {
	switch rt.Kind() {
	case reflect.Pointer:
		pt := rt.Elem()
		pv := reflect.New(rt.Elem()).Elem()
		if setValueFromString(pt, pv, val, key) {
			rv.Set(pv.Addr())
		}
	case reflect.String:
		rv.SetString(val)
	case reflect.Bool:
		rv.SetBool(kitstrings.IsTruthy(val))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(val, 10, 64)
		if err == nil {
			rv.SetInt(v)
		} else {
			log.Warnf("Failed to parse int value from annotation %s (annotation will be ignored): %v", key, err)
			return false
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(val, 10, 64)
		if err == nil {
			rv.SetUint(v)
		} else {
			log.Warnf("Failed to parse uint value from annotation %s (annotation will be ignored): %v", key, err)
			return false
		}
	}

	return true
}

// String implements fmt.Stringer and is used to print the state of the object, primarily for debugging purposes.
func (c *SidecarConfig) String() string {
	return c.toString(false)
}

// StringAll returns the list of all annotations (including empty ones), primarily for debugging purposes.
func (c *SidecarConfig) StringAll() string {
	return c.toString(true)
}

func (c *SidecarConfig) toString(includeAll bool) string {
	res := strings.Builder{}

	// Iterate through the fields using reflection
	val := reflect.ValueOf(c).Elem()
	for i := range val.NumField() {
		fieldT := val.Type().Field(i)
		fieldV := val.Field(i)
		key := fieldT.Tag.Get("annotation")
		def := fieldT.Tag.Get("default")
		if key == "" {
			continue
		}

		// Do not print default values or zero values when there's no default
		val := cast.ToString(fieldV.Interface())
		if includeAll || (def != "" && val != def) || (def == "" && !fieldV.IsZero()) {
			res.WriteString(key + ": " + strconv.Quote(val) + "\n")
		}
	}

	return res.String()
}
