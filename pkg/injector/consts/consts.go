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

package consts

import (
	"github.com/dapr/dapr/pkg/modes"
)

// DaprMode is the runtime mode for Dapr.
type DaprMode = modes.DaprMode

const (
	SidecarContainerName           = "daprd" // Name of the Dapr sidecar container
	SidecarHTTPPortName            = "dapr-http"
	SidecarGRPCPortName            = "dapr-grpc"
	SidecarInternalGRPCPortName    = "dapr-internal"
	SidecarMetricsPortName         = "dapr-metrics"
	SidecarDebugPortName           = "dapr-debug"
	SidecarHealthzPath             = "healthz"
	SidecarInjectedLabel           = "dapr.io/sidecar-injected"
	SidecarAppIDLabel              = "dapr.io/app-id"
	SidecarMetricsEnabledLabel     = "dapr.io/metrics-enabled"
	APIVersionV1                   = "v1.0"
	UnixDomainSocketVolume         = "dapr-unix-domain-socket"              // Name of the UNIX domain socket volume.
	UnixDomainSocketDaprdPath      = "/var/run/dapr-sockets"                // Path in the daprd container where UNIX domain sockets are mounted.
	UserContainerAppProtocolName   = "APP_PROTOCOL"                         // Name of the variable exposed to the app containing the app protocol.
	UserContainerDaprHTTPPortName  = "DAPR_HTTP_PORT"                       // Name of the variable exposed to the app containing the Dapr HTTP port.
	UserContainerDaprGRPCPortName  = "DAPR_GRPC_PORT"                       // Name of the variable exposed to the app containing the Dapr gRPC port.
	TokenVolumeKubernetesMountPath = "/var/run/secrets/dapr.io/sentrytoken" /* #nosec */ // Mount path for the Kubernetes service account volume with the sentry token.
	TokenVolumeName                = "dapr-identity-token"                  /* #nosec */ // Name of the volume with the service account token for daprd.
	ComponentsUDSVolumeName        = "dapr-components-unix-domain-socket"   // Name of the Unix domain socket volume for components.
	ComponentsUDSMountPathEnvVar   = "DAPR_COMPONENT_SOCKETS_FOLDER"
	ComponentsUDSDefaultFolder     = "/tmp/dapr-components-sockets"

	ModeKubernetes = modes.KubernetesMode // KubernetesMode is a Kubernetes Dapr mode.
	ModeStandalone = modes.StandaloneMode // StandaloneMode is a Standalone Dapr mode.
)
