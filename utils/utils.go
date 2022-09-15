/*
Copyright 2021 The Dapr Authors
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
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	clientSet     *kubernetes.Clientset
	kubeConfig    *rest.Config
	KubeConfigVar = "KUBE_CONFIG"
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

	return conf
}

// GetKubeClient gets a kubernetes client.
func GetKubeClient() *kubernetes.Clientset {
	if clientSet == nil {
		initKubeConfig()
	}

	return clientSet
}

// ToISO8601DateTimeString converts dateTime to ISO8601 Format
// ISO8601 Format: 2020-01-01T01:01:01.10101Z.
func ToISO8601DateTimeString(dateTime time.Time) string {
	return dateTime.UTC().Format("2006-01-02T15:04:05.999999Z")
}

// Contains reports whether v is present in s.
// Similar to https://pkg.go.dev/golang.org/x/exp/slices#Contains.
func Contains[T comparable](s []T, v T) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

// SetEnvVariables set variables to environment.
func SetEnvVariables(variables map[string]string) error {
	for key, value := range variables {
		err := os.Setenv(key, value)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetEnvOrElse get the value from the OS environment or use the else value if variable is not present.
func GetEnvOrElse(name, orElse string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return orElse
}

// IsTruthy returns true if a string is a truthy value.
// Truthy values are "y", "yes", "true", "t", "on", "1" (case-insensitive); everything else is false.
func IsTruthy(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "y", "yes", "true", "t", "on", "1":
		return true
	default:
		return false
	}
}

// IsYaml checks whether the file is yaml or not.
func IsYaml(fileName string) bool {
	extension := strings.ToLower(filepath.Ext(fileName))
	if extension == ".yaml" || extension == ".yml" {
		return true
	}
	return false
}
