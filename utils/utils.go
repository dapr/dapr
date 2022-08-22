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
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	clientSet     *kubernetes.Clientset
	kubeConfig    *rest.Config
	KubeConfigVar = "KUBE_CONFIG"

	envRegexp = regexp.MustCompile(`(?m)(,)\s*[a-zA-Z\_][a-zA-Z0-9\_]*=`)
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

// add env-vars from annotations.
func ParseEnvString(envStr string) []corev1.EnvVar {
	indexes := envRegexp.FindAllStringIndex(envStr, -1)
	lastEnd := len(envStr)
	parts := make([]string, len(indexes)+1)
	for i := len(indexes) - 1; i >= 0; i-- {
		parts[i+1] = strings.TrimSpace(envStr[indexes[i][0]+1 : lastEnd])
		lastEnd = indexes[i][0]
	}
	parts[0] = envStr[0:lastEnd]

	envVars := make([]corev1.EnvVar, 0)
	for _, s := range parts {
		pairs := strings.Split(strings.TrimSpace(s), "=")
		if len(pairs) != 2 {
			continue
		}
		envVars = append(envVars, corev1.EnvVar{
			Name:  pairs[0],
			Value: pairs[1],
		})
	}

	return envVars
}

// ParseVolumeMountsString parses the annotation and returns volume mounts.
// The format of the annotation is: "mountPath1:hostPath1,mountPath2:hostPath2"
// The readOnly parameter applies to all mounts.
func ParseVolumeMountsString(volumeMountStr string, readOnly bool) []corev1.VolumeMount {
	volumeMounts := make([]corev1.VolumeMount, 0)

	vs := strings.Split(volumeMountStr, ",")
	for _, v := range vs {
		vmount := strings.Split(strings.TrimSpace(v), ":")
		if len(vmount) != 2 {
			continue
		}
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      vmount[0],
			MountPath: vmount[1],
			ReadOnly:  readOnly,
		})
	}
	return volumeMounts
}

// StringSliceContains return true if an array contains the "str" string.
func StringSliceContains(needle string, haystack []string) bool {
	for _, item := range haystack {
		if item == needle {
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
