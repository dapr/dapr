package modes

// ActionsMode is the runtime mode for Actions
type ActionsMode string

const (
	// KubernetesMode is a Kubernetes Actions mode
	KubernetesMode ActionsMode = "kubernetes"
	// StandaloneMode is a Standalone Actions mode
	StandaloneMode ActionsMode = "standalone"
)
