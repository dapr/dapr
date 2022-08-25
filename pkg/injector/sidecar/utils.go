/*
Copyright 2022 The Dapr Authors
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

package sidecar

import (
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

var envRegexp = regexp.MustCompile(`(?m)(,)\s*[a-zA-Z\_][a-zA-Z0-9\_]*=`)

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
