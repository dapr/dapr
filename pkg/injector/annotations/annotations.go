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

// Annotation keys

const (
	KeyEnabled                                          = "dapr.io/enabled"
	KeyAppPort                                          = "dapr.io/app-port"
	KeyConfig                                           = "dapr.io/config"
	KeyAppProtocol                                      = "dapr.io/app-protocol"
	KeyAppID                                            = "dapr.io/app-id"
	KeyEnableProfiling                                  = "dapr.io/enable-profiling"
	KeyLogLevel                                         = "dapr.io/log-level"
	KeyAPITokenSecret                                   = "dapr.io/api-token-secret" /* #nosec */
	KeyAppTokenSecret                                   = "dapr.io/app-token-secret" /* #nosec */
	KeyLogAsJSON                                        = "dapr.io/log-as-json"
	KeyAppMaxConcurrency                                = "dapr.io/app-max-concurrency"
	KeyEnableMetrics                                    = "dapr.io/enable-metrics"
	KeyMetricsPort                                      = "dapr.io/metrics-port"
	KeyEnableDebug                                      = "dapr.io/enable-debug"
	KeyDebugPort                                        = "dapr.io/debug-port"
	KeyEnv                                              = "dapr.io/env"
	KeyEnableCallbackChannel                            = "dapr.io/enable-callback-channel"
	KeyCallbackChannelPort                              = "dapr.io/callback-channel-port"
	KeyCPULimit                                         = "dapr.io/sidecar-cpu-limit"
	KeyMemoryLimit                                      = "dapr.io/sidecar-memory-limit"
	KeyCPURequest                                       = "dapr.io/sidecar-cpu-request"
	KeyMemoryRequest                                    = "dapr.io/sidecar-memory-request"
	KeySidecarListenAddresses                           = "dapr.io/sidecar-listen-addresses"
	KeyLivenessProbeDelaySeconds                        = "dapr.io/sidecar-liveness-probe-delay-seconds"
	KeyLivenessProbeTimeoutSeconds                      = "dapr.io/sidecar-liveness-probe-timeout-seconds"
	KeyLivenessProbePeriodSeconds                       = "dapr.io/sidecar-liveness-probe-period-seconds"
	KeyLivenessProbeThreshold                           = "dapr.io/sidecar-liveness-probe-threshold"
	KeyReadinessProbeDelaySeconds                       = "dapr.io/sidecar-readiness-probe-delay-seconds"
	KeyReadinessProbeTimeoutSeconds                     = "dapr.io/sidecar-readiness-probe-timeout-seconds"
	KeyReadinessProbePeriodSeconds                      = "dapr.io/sidecar-readiness-probe-period-seconds"
	KeyReadinessProbeThreshold                          = "dapr.io/sidecar-readiness-probe-threshold"
	KeySidecarImage                                     = "dapr.io/sidecar-image"
	KeyAppSSL                                           = "dapr.io/app-ssl"
	KeyHTTPMaxRequestBodySize                           = "dapr.io/http-max-request-size"
	KeyHTTPReadBufferSize                               = "dapr.io/http-read-buffer-size"
	KeyGracefulShutdownSeconds                          = "dapr.io/graceful-shutdown-seconds"
	KeyEnableAPILogging                                 = "dapr.io/enable-api-logging"
	KeyUnixDomainSocketPath                             = "dapr.io/unix-domain-socket-path"
	KeyVolumeMountsReadOnly                             = "dapr.io/volume-mounts"
	KeyVolumeMountsReadWrite                            = "dapr.io/volume-mounts-rw"
	KeyDisableBuiltinK8sSecretStore                     = "dapr.io/disable-builtin-k8s-secret-store" //nolint:gosec
	KeyEnableAppHealthCheck                             = "dapr.io/enable-app-health-check"
	KeyAppHealthCheckPath                               = "dapr.io/app-health-check-path"
	KeyAppHealthProbeInterval                           = "dapr.io/app-health-probe-interval"
	KeyAppHealthProbeTimeout                            = "dapr.io/app-health-probe-timeout"
	KeyAppHealthThreshold                               = "dapr.io/app-health-threshold"
	KeyPlacementHostAddresses                           = "dapr.io/placement-host-address"
	KeyPluggableComponents                              = "dapr.io/pluggable-components"
	KeyPluggableComponentsSocketsFolder                 = "dapr.io/pluggable-components-sockets-folder"
	KeyPluggableComponentsInjection                     = "dapr.io/inject-pluggable-components"
	KeyPluggableComponentContainerImage                 = "dapr.io/component-container-image"
	KeyPluggableComponentContainerVolumeMountsReadOnly  = "dapr.io/component-container-volume-mounts"
	KeyPluggableComponentContainerVolumeMountsReadWrite = "dapr.io/component-container-volume-mounts-rw"
	KeyPluggableComponentContainerEnvironment           = "dapr.io/component-container-env"
)

// Default values

const (
	DefaultLogLevel                          = "info"
	DefaultLogAsJSON                         = false
	DefaultAppSSL                            = false
	DefaultAppProtocol                       = "http"
	DefaultEnableMetric                      = true
	DefaultMetricsPort                       = 9090
	DefaultEnableDebug                       = false
	DefaultDebugPort                         = 40000
	DefaultSidecarListenAddresses            = "[::1],127.0.0.1"
	DefaultHealthzProbeDelaySeconds          = 3
	DefaultHealthzProbeTimeoutSeconds        = 3
	DefaultHealthzProbePeriodSeconds         = 6
	DefaultHealthzProbeThreshold             = 3
	DefaultMtlsEnabled                       = true
	DefaultEnableProfiling                   = false
	DefaultDisableBuiltinK8sSecretStore      = false
	DefaultEnableAppHealthCheck              = false
	DefaultAppCheckPath                      = "/health"
	DefaultAppHealthProbeIntervalSeconds     = 5
	DefaultAppHealthProbeTimeoutMilliseconds = 500
	DefaultAppHealthThreshold                = 3
)
