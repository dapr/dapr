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

package utils

import (
	"os"
	"strconv"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	clientSet          *kubernetes.Clientset
	kubeConfig         *rest.Config
	KubeConfigVar      = "KUBE_CONFIG"
	KubeAPIServerQPS   = "KUBE_API_SERVER_QPS"
	KubeAPIServerBurst = "KUBE_API_SERVER_BURST"
)

func initKubeConfig() {
	kubeConfig = GetConfig()
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		panic(err)
	}

	clientSet = clientset
}

// GetConfig gets a kubernetes rest config.
func GetConfig() *rest.Config {
	if kubeConfig != nil {
		return kubeConfig
	}

	conf, err := rest.InClusterConfig()
	if err != nil {
		conf, err = clientcmd.BuildConfigFromFlags("", os.Getenv(KubeConfigVar))
		if err != nil {
			panic(err)
		}
	}

	if kubeAPIServerQPS := os.Getenv(KubeAPIServerQPS); kubeAPIServerQPS != "" {
		qps, err := strconv.ParseFloat(kubeAPIServerQPS, 32)
		if err != nil {
			panic("KUBE_API_SERVER_QPS must be a valid float value")
		}
		conf.QPS = float32(qps)
	}

	if kubeAPIServerBurst := os.Getenv(KubeAPIServerBurst); kubeAPIServerBurst != "" {
		burst, err := strconv.Atoi(kubeAPIServerBurst)
		if err != nil {
			panic("KUBE_API_SERVER_BURST must be a valid integer value")
		}
		conf.Burst = burst
	}

	return conf
}

// GetKubeClient gets a kubernetes client.
func GetKubeClient() *kubernetes.Clientset {
	if clientSet == nil {
		initKubeConfig()
	}

	return clientSet
}
