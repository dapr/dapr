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

// Package annotations contains the list of annotations for Dapr deployments.
package annotations

// Annotation keys.
// Name must start with "Key".

const (
	KeyEnabled                          = "dapr.io/enabled"
	KeyAppPort                          = "dapr.io/app-port"
	KeyConfig                           = "dapr.io/config"
	KeyAppProtocol                      = "dapr.io/app-protocol"
	KeyAppSSL                           = "dapr.io/app-ssl" // Deprecated. Remove in a future Dapr version. Use "app-protocol" with "https" or "grpcs"
	KeyAppID                            = "dapr.io/app-id"
	KeyEnableProfiling                  = "dapr.io/enable-profiling"
	KeyLogLevel                         = "dapr.io/log-level"
	KeyAPITokenSecret                   = "dapr.io/api-token-secret" /* #nosec */
	KeyAppTokenSecret                   = "dapr.io/app-token-secret" /* #nosec */
	KeyLogAsJSON                        = "dapr.io/log-as-json"
	KeyAppMaxConcurrency                = "dapr.io/app-max-concurrency"
	KeyEnableMetrics                    = "dapr.io/enable-metrics"
	KeyMetricsPort                      = "dapr.io/metrics-port"
	KeyEnableDebug                      = "dapr.io/enable-debug"
	KeyDebugPort                        = "dapr.io/debug-port"
	KeyEnv                              = "dapr.io/env"
	KeyEnvFromSecret                    = "dapr.io/env-from-secret" //nolint:gosec
	KeyAPIGRPCPort                      = "dapr.io/grpc-port"
	KeyInternalGRPCPort                 = "dapr.io/internal-grpc-port"
	KeyCPURequest                       = "dapr.io/sidecar-cpu-request"
	KeyCPULimit                         = "dapr.io/sidecar-cpu-limit"
	KeyMemoryRequest                    = "dapr.io/sidecar-memory-request"
	KeyMemoryLimit                      = "dapr.io/sidecar-memory-limit"
	KeySidecarListenAddresses           = "dapr.io/sidecar-listen-addresses"
	KeyLivenessProbeDelaySeconds        = "dapr.io/sidecar-liveness-probe-delay-seconds"
	KeyLivenessProbeTimeoutSeconds      = "dapr.io/sidecar-liveness-probe-timeout-seconds"
	KeyLivenessProbePeriodSeconds       = "dapr.io/sidecar-liveness-probe-period-seconds"
	KeyLivenessProbeThreshold           = "dapr.io/sidecar-liveness-probe-threshold"
	KeyReadinessProbeDelaySeconds       = "dapr.io/sidecar-readiness-probe-delay-seconds"
	KeyReadinessProbeTimeoutSeconds     = "dapr.io/sidecar-readiness-probe-timeout-seconds"
	KeyReadinessProbePeriodSeconds      = "dapr.io/sidecar-readiness-probe-period-seconds"
	KeyReadinessProbeThreshold          = "dapr.io/sidecar-readiness-probe-threshold"
	KeySidecarImage                     = "dapr.io/sidecar-image"
	KeySidecarSeccompProfileType        = "dapr.io/sidecar-seccomp-profile-type"
	KeyHTTPMaxRequestSize               = "dapr.io/http-max-request-size"
	KeyMaxBodySize                      = "dapr.io/max-body-size"
	KeyHTTPReadBufferSize               = "dapr.io/http-read-buffer-size"
	KeyReadBufferSize                   = "dapr.io/read-buffer-size"
	KeyGracefulShutdownSeconds          = "dapr.io/graceful-shutdown-seconds"
	KeyBlockShutdownDuration            = "dapr.io/block-shutdown-duration"
	KeyEnableAPILogging                 = "dapr.io/enable-api-logging"
	KeyUnixDomainSocketPath             = "dapr.io/unix-domain-socket-path"
	KeyVolumeMountsReadOnly             = "dapr.io/volume-mounts"
	KeyVolumeMountsReadWrite            = "dapr.io/volume-mounts-rw"
	KeyDisableBuiltinK8sSecretStore     = "dapr.io/disable-builtin-k8s-secret-store" //nolint:gosec
	KeyEnableAppHealthCheck             = "dapr.io/enable-app-health-check"
	KeyAppHealthCheckPath               = "dapr.io/app-health-check-path"
	KeyAppHealthProbeInterval           = "dapr.io/app-health-probe-interval"
	KeyAppHealthProbeTimeout            = "dapr.io/app-health-probe-timeout"
	KeyAppHealthThreshold               = "dapr.io/app-health-threshold"
	KeyPlacementHostAddresses           = "dapr.io/placement-host-address"
	KeySchedulerHostAddresses           = "dapr.io/scheduler-host-address"
	KeyPluggableComponents              = "dapr.io/pluggable-components"
	KeyPluggableComponentsSocketsFolder = "dapr.io/pluggable-components-sockets-folder"
	KeyPluggableComponentContainer      = "dapr.io/component-container"
	KeyPluggableComponentsInjection     = "dapr.io/inject-pluggable-components"
	KeyAppChannel                       = "dapr.io/app-channel-address"
	KeySentryRequestJwtAudiences        = "dapr.io/sentry-request-jwt-audiences"
)
