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

// package sidecar contains helpers to build the Container object for Kubernetes to deploy the Dapr sidecar container.
package sidecar

const (
	SidecarContainerName          = "daprd" // Name of the Dapr sidecar container
	SidecarHTTPPort               = 3500
	SidecarAPIGRPCPort            = 50001
	SidecarInternalGRPCPort       = 50002
	SidecarPublicPort             = 3501
	SidecarHTTPPortName           = "dapr-http"
	SidecarGRPCPortName           = "dapr-grpc"
	SidecarInternalGRPCPortName   = "dapr-internal"
	SidecarMetricsPortName        = "dapr-metrics"
	SidecarDebugPortName          = "dapr-debug"
	SidecarHealthzPath            = "healthz"
	APIVersionV1                  = "v1.0"
	UnixDomainSocketVolume        = "dapr-unix-domain-socket" // Name of the Unix domain socket volume.
	UserContainerDaprHTTPPortName = "DAPR_HTTP_PORT"          // Name of the variable exposed to the app containing the Dapr HTTP port.
	UserContainerDaprGRPCPortName = "DAPR_GRPC_PORT"          // Name of the variable exposed to the app containing the Dapr gRPC port.
	ContainersPath                = "/spec/containers"
	KubernetesMountPath           = "/var/run/secrets/kubernetes.io/serviceaccount" // Mount path for the Kubernetes service account volume.
)
