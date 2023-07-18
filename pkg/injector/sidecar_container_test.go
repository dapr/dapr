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

package injector

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/stretchr/testify/assert"
)

func TestParseEnvString(t *testing.T) {
	testCases := []struct {
		testName string
		envStr   string
		expLen   int
		expKeys  []string
		expEnv   []corev1.EnvVar
	}{
		{
			testName: "empty environment string",
			envStr:   "",
			expLen:   0,
			expKeys:  []string{},
			expEnv:   []corev1.EnvVar{},
		},
		{
			testName: "common valid environment string",
			envStr:   "ENV1=value1,ENV2=value2, ENV3=value3",
			expLen:   3,
			expKeys:  []string{"ENV1", "ENV2", "ENV3"},
			expEnv: []corev1.EnvVar{
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
			expEnv: []corev1.EnvVar{
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
			c := NewSidecarConfig()
			c.Env = tc.envStr
			envKeys, envVars := c.GetEnv()
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
		expMounts    []corev1.VolumeMount
	}{
		{
			testName:     "empty volume mount string.",
			mountStr:     "",
			readOnly:     false,
			expMountsLen: 0,
			expMounts:    []corev1.VolumeMount{},
		},
		{
			testName:     "valid volume mount string with readonly false.",
			mountStr:     "my-mount:/tmp/mount1,another-mount:/home/user/mount2, mount3:/root/mount3",
			readOnly:     false,
			expMountsLen: 3,
			expMounts: []corev1.VolumeMount{
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
			expMounts: []corev1.VolumeMount{
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
			expMounts: []corev1.VolumeMount{
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
			mounts := parseVolumeMountsString(tc.mountStr, tc.readOnly)
			assert.Equal(t, tc.expMountsLen, len(mounts))
			assert.Equal(t, tc.expMounts, mounts)
		})
	}
}

func TestPodContainsVolume(t *testing.T) {
	testCases := []struct {
		testName   string
		podVolumes []corev1.Volume
		volumeName string
		expect     bool
	}{
		{
			"pod with no volumes",
			[]corev1.Volume{},
			"volume1",
			false,
		},
		{
			"pod does not contain volume",
			[]corev1.Volume{
				{Name: "volume"},
			},
			"volume1",
			false,
		},
		{
			"pod contains volume",
			[]corev1.Volume{
				{Name: "volume1"},
				{Name: "volume2"},
			},
			"volume2",
			true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: tc.podVolumes,
				},
			}
			assert.Equal(t, tc.expect, podContainsVolume(pod, tc.volumeName))
		})
	}
}

func TestGetVolumeMounts(t *testing.T) {
	testCases := []struct {
		testName                  string
		volumeReadOnlyAnnotation  string
		volumeReadWriteAnnotation string
		podVolumeMountNames       []string
		expVolumeMounts           []corev1.VolumeMount
	}{
		{
			"no annotations",
			"",
			"",
			[]string{"mount1", "mount2"},
			[]corev1.VolumeMount{},
		},
		{
			"annotations with volumes present in pod",
			"mount1:/tmp/mount1,mount2:/tmp/mount2",
			"mount3:/tmp/mount3,mount4:/tmp/mount4",
			[]string{"mount1", "mount2", "mount3", "mount4"},
			[]corev1.VolumeMount{
				{Name: "mount1", MountPath: "/tmp/mount1", ReadOnly: true},
				{Name: "mount2", MountPath: "/tmp/mount2", ReadOnly: true},
				{Name: "mount3", MountPath: "/tmp/mount3", ReadOnly: false},
				{Name: "mount4", MountPath: "/tmp/mount4", ReadOnly: false},
			},
		},
		{
			"annotations with volumes not present in pod",
			"mount1:/tmp/mount1,mount2:/tmp/mount2",
			"mount3:/tmp/mount3,mount4:/tmp/mount4",
			[]string{"mount1", "mount2", "mount4"},
			[]corev1.VolumeMount{
				{Name: "mount1", MountPath: "/tmp/mount1", ReadOnly: true},
				{Name: "mount2", MountPath: "/tmp/mount2", ReadOnly: true},
				{Name: "mount4", MountPath: "/tmp/mount4", ReadOnly: false},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			c := NewSidecarConfig()
			c.VolumeMounts = tc.volumeReadOnlyAnnotation
			c.VolumeMountsRW = tc.volumeReadWriteAnnotation

			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{},
				},
			}
			for _, volumeName := range tc.podVolumeMountNames {
				pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: volumeName})
			}

			volumeMounts := c.GetVolumeMounts(pod)
			assert.Equal(t, tc.expVolumeMounts, volumeMounts)
		})
	}
}

func TestGetUnixDomainSocketVolume(t *testing.T) {
	testCases := []struct {
		testName        string
		udsPath         string
		originalVolumes []corev1.Volume
		expectVolumes   []corev1.Volume
		exportMount     *corev1.VolumeMount
	}{
		{
			"empty value",
			"",
			nil,
			nil,
			nil,
		},
		{
			"append on empty volumes",
			"/tmp",
			nil,
			[]corev1.Volume{{Name: UnixDomainSocketVolume}},
			&corev1.VolumeMount{Name: UnixDomainSocketVolume, MountPath: "/tmp"},
		},
		{
			"append on existed volumes",
			"/tmp",
			[]corev1.Volume{{Name: "mock"}},
			[]corev1.Volume{{Name: UnixDomainSocketVolume}, {Name: "mock"}},
			&corev1.VolumeMount{Name: UnixDomainSocketVolume, MountPath: "/tmp"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			c := NewSidecarConfig()
			c.UnixDomainSocketPath = tc.udsPath

			pod := corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: tc.originalVolumes,
				},
			}

			socketMount := c.GetUnixDomainSocketVolumeMount(&pod)

			if tc.exportMount == nil {
				assert.Equal(t, tc.exportMount, socketMount)
			} else {
				assert.Equal(t, tc.exportMount.Name, socketMount.Name)
				assert.Equal(t, tc.exportMount.MountPath, socketMount.MountPath)
			}

			assert.Equal(t, len(tc.expectVolumes), len(pod.Spec.Volumes))
		})
	}
}
