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

package patcher

import (
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"

	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
)

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
			assert.Len(t, mounts, tc.expMountsLen)
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
			pod := &corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{},
				},
			}
			for _, volumeName := range tc.podVolumeMountNames {
				pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: volumeName})
			}

			c := NewSidecarConfig(pod)
			c.VolumeMounts = tc.volumeReadOnlyAnnotation
			c.VolumeMountsRW = tc.volumeReadWriteAnnotation

			volumeMounts := c.getVolumeMounts()
			assert.Equal(t, tc.expVolumeMounts, volumeMounts)
		})
	}
}

func TestGetUnixDomainSocketVolume(t *testing.T) {
	c := NewSidecarConfig(&corev1.Pod{})
	c.UnixDomainSocketPath = "/tmp"

	volume, daprdMount, appMount := c.getUnixDomainSocketVolumeMount()

	assert.Equal(t, injectorConsts.UnixDomainSocketVolume, volume.Name)
	assert.NotNil(t, volume.VolumeSource.EmptyDir)
	assert.Equal(t, injectorConsts.UnixDomainSocketVolume, daprdMount.Name)
	assert.Equal(t, injectorConsts.UnixDomainSocketDaprdPath, daprdMount.MountPath)
	assert.Equal(t, injectorConsts.UnixDomainSocketVolume, appMount.Name)
	assert.Equal(t, "/tmp", appMount.MountPath)
}

func TestAddVolumeToContainers(t *testing.T) {
	testCases := []struct {
		testName      string
		mockContainer corev1.Container
		socketMount   corev1.VolumeMount
		expOpsLen     int
		expOps        jsonpatch.Patch
	}{
		{
			testName: "existing var, empty volume",
			mockContainer: corev1.Container{
				Name: "MockContainer",
			},
			socketMount: corev1.VolumeMount{
				Name:      injectorConsts.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: jsonpatch.Patch{
				NewPatchOperation("add", "/spec/containers/0/volumeMounts", []corev1.VolumeMount{{
					Name:      injectorConsts.UnixDomainSocketVolume,
					MountPath: "/tmp",
				}}),
			},
		},
		{
			testName: "existing var, existing volume",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{Name: "mock1"},
				},
			},
			socketMount: corev1.VolumeMount{
				Name:      injectorConsts.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: jsonpatch.Patch{
				NewPatchOperation("add", "/spec/containers/0/volumeMounts/-", corev1.VolumeMount{
					Name:      injectorConsts.UnixDomainSocketVolume,
					MountPath: "/tmp",
				}),
			},
		},
		{
			testName: "existing var, multiple existing volumes",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{Name: "mock1"},
					{Name: "mock2"},
				},
			},
			socketMount: corev1.VolumeMount{
				Name:      injectorConsts.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: jsonpatch.Patch{
				NewPatchOperation("add", "/spec/containers/0/volumeMounts/-", corev1.VolumeMount{
					Name:      injectorConsts.UnixDomainSocketVolume,
					MountPath: "/tmp",
				}),
			},
		},
		{
			testName: "existing var, conflict volume name",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{Name: injectorConsts.UnixDomainSocketVolume},
				},
			},
			socketMount: corev1.VolumeMount{
				Name:      injectorConsts.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 0,
			expOps:    jsonpatch.Patch{},
		},
		{
			testName: "existing var, conflict volume mount path",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{MountPath: "/tmp"},
				},
			},
			socketMount: corev1.VolumeMount{
				Name:      injectorConsts.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 0,
			expOps:    jsonpatch.Patch{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			patchEnv := addVolumeMountToContainers(map[int]corev1.Container{0: tc.mockContainer}, tc.socketMount)
			assert.Len(t, patchEnv, tc.expOpsLen)
			assert.Equal(t, tc.expOps, patchEnv)
		})
	}
}
