// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package utils

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var clientSet *kubernetes.Clientset
var kubeConfig *rest.Config

func initKubeConfig() {
	kubeConfig = GetConfig()
	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		panic(err)
	}

	clientSet = clientset
}

// GetConfig gets a kubernetes rest config
func GetConfig() *rest.Config {
	if kubeConfig != nil {
		return kubeConfig
	}

	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	conf, err := rest.InClusterConfig()
	if err != nil {
		conf, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err)
		}
	}

	return conf
}

// GetKubeClient gets a kubernetes client
func GetKubeClient() *kubernetes.Clientset {
	if clientSet == nil {
		initKubeConfig()
	}

	return clientSet
}

// ToISO8601DateTimeString converts dateTime to ISO8601 Format
// ISO8601 Format: 2020-01-01T01:01:01.10101Z
func ToISO8601DateTimeString(dateTime time.Time) string {
	t := dateTime.UTC()

	year, month, day := t.Date()
	hour, minute, second := t.Clock()
	micros := t.Nanosecond() / 1000

	return fmt.Sprintf(
		"%04d-%02d-%02dT%02d:%02d:%02d.%03dZ",
		year, month, day, hour, minute, second, micros)
}

// add env-vars from annotations.
// see https://github.com/dapr/dapr/issues/2508.
func ParseEnvString(envStr string) []corev1.EnvVar {
	envVars := make([]corev1.EnvVar, 0)
	envPairs := strings.Split(envStr, ",")

	for _, value := range envPairs {
		pair := strings.Split(strings.TrimSpace(value), "=")

		if len(pair) != 2 {
			continue
		}

		envVars = append(envVars, corev1.EnvVar{
			Name:  pair[0],
			Value: pair[1],
		})
	}

	return envVars
}

// StringSliceContains return true if an array containe the "str" string
func StringSliceContains(needle string, haystack []string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
