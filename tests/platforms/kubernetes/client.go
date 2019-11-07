// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	appv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	apiv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// KubeClient holds instances of Kubernetes clientset
// TODO: Add cluster management methods to clean up the old test apps
type KubeClient struct {
	ClientSet kubernetes.Interface
}

// NewKubeClient creates KubeClient instance
func NewKubeClient(configPath string, clusterName string) (*KubeClient, error) {
	config, err := clientConfig(configPath, clusterName)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &KubeClient{ClientSet: clientset}, nil
}

func clientConfig(kubeConfigPath string, clusterName string) (*rest.Config, error) {
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

// Deployments gets Deployment client for namespace
func (c *KubeClient) Deployments(namespace string) appv1.DeploymentInterface {
	return c.ClientSet.AppsV1().Deployments(namespace)
}

// Services gets Service client for namespace
func (c *KubeClient) Services(namespace string) apiv1.ServiceInterface {
	return c.ClientSet.CoreV1().Services(namespace)
}

// Pods gets Pod client for namespace
func (c *KubeClient) Pods(namespace string) apiv1.PodInterface {
	return c.ClientSet.CoreV1().Pods(namespace)
}
