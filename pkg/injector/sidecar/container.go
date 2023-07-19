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
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/kit/logger"
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
	SkipPlacement                bool
	RunAsNonRoot                 bool
	ReadOnlyRootFilesystem       bool
	SidecarDropALLCapabilities   bool
}

var (
	log = logger.NewLogger("dapr.injector.container")
)
