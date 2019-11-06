package kubernetes

import (
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// KubeClients holds instances of Kubernetes clientset
type KubeClients struct {
	ClientSet *kubernetes.Clientset
}

// NewKubeClients creates KubeClients instance
func NewKubeClients(configPath string, clusterName string) (*KubeClients, error) {
	config, err := getClientConfig(configPath, clusterName)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &KubeClients{ClientSet: clientset}, nil
}

func getClientConfig(kubeConfigPath string, clusterName string) (*rest.Config, error) {
	if kubeConfigPath == "" {
		if home := homedir.HomeDir(); home != "" {
			kubeConfigPath = filepath.Join(home, ".kube", "config")
		}
	}

	overrides := clientcmd.ConfigOverrides{}

	if clusterName != "" {
		overrides.Context.Cluster = clusterName
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeConfigPath},
		&overrides).ClientConfig()
}
