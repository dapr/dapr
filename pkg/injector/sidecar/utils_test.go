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
	"testing"

	"github.com/stretchr/testify/assert"
	coreV1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/pkg/injector/annotations"
)

func TestGetAppID(t *testing.T) {
	t.Run("get app id", func(t *testing.T) {
		m := map[string]string{annotations.KeyAppID: "app"}
		pod := coreV1.Pod{}
		pod.Annotations = m
		id := GetAppID(pod.ObjectMeta)
		assert.Equal(t, "app", id)
	})

	t.Run("get pod id", func(t *testing.T) {
		pod := coreV1.Pod{}
		pod.ObjectMeta.Name = "pod"
		id := GetAppID(pod.ObjectMeta)
		assert.Equal(t, "pod", id)
	})
}

func TestParseEnvString(t *testing.T) {
	testCases := []struct {
		testName string
		envStr   string
		expLen   int
		expKeys  []string
		expEnv   []coreV1.EnvVar
	}{
		{
			testName: "empty environment string",
			envStr:   "",
			expLen:   0,
			expKeys:  []string{},
			expEnv:   []coreV1.EnvVar{},
		},
		{
			testName: "common valid environment string",
			envStr:   "ENV1=value1,ENV2=value2, ENV3=value3",
			expLen:   3,
			expKeys:  []string{"ENV1", "ENV2", "ENV3"},
			expEnv: []coreV1.EnvVar{
				{
					Name:  "ENV1",
					Value: "value1",
				},
				{
					Name:  "ENV2",
					Value: "value2",
				},
				{
					Name:  "ENV3",
					Value: "value3",
				},
			},
		},
		{
			testName: "environment string with quotes",
			envStr:   `HTTP_PROXY=http://myproxy.com, NO_PROXY="localhost,127.0.0.1,.amazonaws.com"`,
			expLen:   2,
			expKeys:  []string{"HTTP_PROXY", "NO_PROXY"},
			expEnv: []coreV1.EnvVar{
				{
					Name:  "HTTP_PROXY",
					Value: "http://myproxy.com",
				},
				{
					Name:  "NO_PROXY",
					Value: `"localhost,127.0.0.1,.amazonaws.com"`,
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			envKeys, envVars := ParseEnvString(tc.envStr)
			assert.Equal(t, tc.expLen, len(envVars))
			assert.Equal(t, tc.expKeys, envKeys)
			assert.Equal(t, tc.expEnv, envVars)
		})
	}
}

func TestParseVolumeMountsString(t *testing.T) {
	testCases := []struct {
		testName     string
		mountStr     string
		readOnly     bool
		expMountsLen int
		expMounts    []coreV1.VolumeMount
	}{
		{
			testName:     "empty volume mount string.",
			mountStr:     "",
			readOnly:     false,
			expMountsLen: 0,
			expMounts:    []coreV1.VolumeMount{},
		},
		{
			testName:     "valid volume mount string with readonly false.",
			mountStr:     "my-mount:/tmp/mount1,another-mount:/home/user/mount2, mount3:/root/mount3",
			readOnly:     false,
			expMountsLen: 3,
			expMounts: []coreV1.VolumeMount{
				{
					Name:      "my-mount",
					MountPath: "/tmp/mount1",
				},
				{
					Name:      "another-mount",
					MountPath: "/home/user/mount2",
				},
				{
					Name:      "mount3",
					MountPath: "/root/mount3",
				},
			},
		},
		{
			testName:     "valid volume mount string with readonly true.",
			mountStr:     " my-mount:/tmp/mount1,mount2:/root/mount2 ",
			readOnly:     true,
			expMountsLen: 2,
			expMounts: []coreV1.VolumeMount{
				{
					Name:      "my-mount",
					MountPath: "/tmp/mount1",
					ReadOnly:  true,
				},
				{
					Name:      "mount2",
					MountPath: "/root/mount2",
					ReadOnly:  true,
				},
			},
		},
		{
			testName:     "volume mount string with invalid mounts",
			mountStr:     "my-mount:/tmp/mount1:rw,mount2:/root/mount2,mount3",
			readOnly:     false,
			expMountsLen: 1,
			expMounts: []coreV1.VolumeMount{
				{
					Name:      "mount2",
					MountPath: "/root/mount2",
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			mounts := ParseVolumeMountsString(tc.mountStr, tc.readOnly)
			assert.Equal(t, tc.expMountsLen, len(mounts))
			assert.Equal(t, tc.expMounts, mounts)
		})
	}
}
