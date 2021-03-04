// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package modes

// DaprMode is the runtime mode for Dapr.
type DaprMode string

const (
	// KubernetesMode is a Kubernetes Dapr mode
	KubernetesMode DaprMode = "kubernetes"
	// OrchestratorMode is a Dapr mode when we run in orchestrator without operator
	// It's use dns name resolution and file configuration
	OrchestratorMode DaprMode = "orchestrator"
	// StandaloneMode is a Standalone Dapr mode
	StandaloneMode DaprMode = "standalone"
)
