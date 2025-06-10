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
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	clientSet      *kubernetes.Clientset
	kubeConfig     *rest.Config
	kubeConfigOnce sync.Once
	KubeConfigVar  = "KUBE_CONFIG"
)

// GetConfig gets a kubernetes rest config.
func GetConfig() *rest.Config {
	if kubeConfig == nil {
		kubeConfigOnce.Do(func() {
			conf, err := rest.InClusterConfig()
			if err != nil {
				conf, err = clientcmd.BuildConfigFromFlags("", os.Getenv(KubeConfigVar))
				if err != nil {
					panic(err)
				}
			}
			kubeConfig = conf
		})
	}

	return kubeConfig
}

// GetKubeClient gets a kubernetes client.
func GetKubeClient(conf *rest.Config) *kubernetes.Clientset {
	if clientSet == nil {
		clientset, err := kubernetes.NewForConfig(conf)
		if err != nil {
			panic(err)
		}
		clientSet = clientset
	}

	return clientSet
}
