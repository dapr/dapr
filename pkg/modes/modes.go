// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package modes

// DaprMode is the runtime mode for Dapr.
type DaprMode string

const (
	// KubernetesMode is a Kubernetes Dapr mode
	KubernetesMode DaprMode = "kubernetes"
	// StandaloneMode is a Standalone Dapr mode
	StandaloneMode DaprMode = "standalone"
)
