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

//nolint:goconst
package injector

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/sidecar"
	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
)

//nolint:forbidigo
func TestAddDaprEnvVarsToContainers(t *testing.T) {
	testCases := []struct {
		testName      string
		mockContainer corev1.Container
		expOpsLen     int
		expOps        []PatchOperation
	}{
		{
			testName: "empty environment vars",
			mockContainer: corev1.Container{
				Name: "MockContainer",
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env",
					Value: []corev1.EnvVar{
						{
							Name:  sidecar.UserContainerDaprHTTPPortName,
							Value: strconv.Itoa(sidecar.SidecarHTTPPort),
						},
						{
							Name:  sidecar.UserContainerDaprGRPCPortName,
							Value: strconv.Itoa(sidecar.SidecarAPIGRPCPort),
						},
					},
				},
			},
		},
		{
			testName: "existing env var",
			mockContainer: corev1.Container{
				Name: "Mock Container",
				Env: []corev1.EnvVar{
					{
						Name:  "TEST",
						Value: "Existing value",
					},
				},
			},
			expOpsLen: 2,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env/-",
					Value: corev1.EnvVar{
						Name:  sidecar.UserContainerDaprHTTPPortName,
						Value: strconv.Itoa(sidecar.SidecarHTTPPort),
					},
				},
				{
					Op:   "add",
					Path: "/spec/containers/0/env/-",
					Value: corev1.EnvVar{
						Name:  sidecar.UserContainerDaprGRPCPortName,
						Value: strconv.Itoa(sidecar.SidecarAPIGRPCPort),
					},
				},
			},
		},
		{
			testName: "existing conflicting env var",
			mockContainer: corev1.Container{
				Name: "Mock Container",
				Env: []corev1.EnvVar{
					{
						Name:  "TEST",
						Value: "Existing value",
					},
					{
						Name:  sidecar.UserContainerDaprGRPCPortName,
						Value: "550000",
					},
				},
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/env/-",
					Value: corev1.EnvVar{
						Name:  sidecar.UserContainerDaprHTTPPortName,
						Value: strconv.Itoa(sidecar.SidecarHTTPPort),
					},
				},
			},
		},
		{
			testName: "multiple existing conflicting env vars",
			mockContainer: corev1.Container{
				Name: "Mock Container",
				Env: []corev1.EnvVar{
					{
						Name:  sidecar.UserContainerDaprHTTPPortName,
						Value: "3510",
					},
					{
						Name:  sidecar.UserContainerDaprGRPCPortName,
						Value: "550000",
					},
				},
			},
			expOpsLen: 0,
			expOps:    []PatchOperation{},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			patchEnv := addDaprEnvVarsToContainers([]corev1.Container{tc.mockContainer})
			fmt.Println(tc.testName)
			assert.Equal(t, tc.expOpsLen, len(patchEnv))
			assert.Equal(t, tc.expOps, patchEnv)
		})
	}
}

func TestAddSocketVolumeToContainers(t *testing.T) {
	testCases := []struct {
		testName      string
		mockContainer corev1.Container
		socketMount   *corev1.VolumeMount
		expOpsLen     int
		expOps        []PatchOperation
	}{
		{
			testName: "empty var, empty volume",
			mockContainer: corev1.Container{
				Name: "MockContainer",
			},
			socketMount: nil,
			expOpsLen:   0,
			expOps:      []PatchOperation{},
		},
		{
			testName: "existing var, empty volume",
			mockContainer: corev1.Container{
				Name: "MockContainer",
			},
			socketMount: &corev1.VolumeMount{
				Name:      sidecar.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/volumeMounts",
					Value: []corev1.VolumeMount{{
						Name:      sidecar.UnixDomainSocketVolume,
						MountPath: "/tmp",
					}},
				},
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
			socketMount: &corev1.VolumeMount{
				Name:      sidecar.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/volumeMounts/-",
					Value: corev1.VolumeMount{
						Name:      sidecar.UnixDomainSocketVolume,
						MountPath: "/tmp",
					},
				},
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
			socketMount: &corev1.VolumeMount{
				Name:      sidecar.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 1,
			expOps: []PatchOperation{
				{
					Op:   "add",
					Path: "/spec/containers/0/volumeMounts/-",
					Value: corev1.VolumeMount{
						Name:      sidecar.UnixDomainSocketVolume,
						MountPath: "/tmp",
					},
				},
			},
		},
		{
			testName: "existing var, conflict volume name",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{Name: sidecar.UnixDomainSocketVolume},
				},
			},
			socketMount: &corev1.VolumeMount{
				Name:      sidecar.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 0,
			expOps:    []PatchOperation{},
		},
		{
			testName: "existing var, conflict volume mount path",
			mockContainer: corev1.Container{
				Name: "MockContainer",
				VolumeMounts: []corev1.VolumeMount{
					{MountPath: "/tmp"},
				},
			},
			socketMount: &corev1.VolumeMount{
				Name:      sidecar.UnixDomainSocketVolume,
				MountPath: "/tmp",
			},
			expOpsLen: 0,
			expOps:    []PatchOperation{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			patchEnv := addSocketVolumeToContainers([]corev1.Container{tc.mockContainer}, tc.socketMount)
			assert.Equal(t, tc.expOpsLen, len(patchEnv))
			assert.Equal(t, tc.expOps, patchEnv)
		})
	}
}

func TestAppendUnixDomainSocketVolume(t *testing.T) {
	testCases := []struct {
		testName        string
		annotations     map[string]string
		originalVolumes []corev1.Volume
		expectVolumes   []corev1.Volume
		exportMount     *corev1.VolumeMount
	}{
		{
			"empty value",
			map[string]string{annotations.KeyUnixDomainSocketPath: ""},
			nil,
			nil,
			nil,
		},
		{
			"append on empty volumes",
			map[string]string{annotations.KeyUnixDomainSocketPath: "/tmp"},
			nil,
			[]corev1.Volume{{
				Name: sidecar.UnixDomainSocketVolume,
			}},
			&corev1.VolumeMount{Name: sidecar.UnixDomainSocketVolume, MountPath: "/tmp"},
		},
		{
			"append on existed volumes",
			map[string]string{annotations.KeyUnixDomainSocketPath: "/tmp"},
			[]corev1.Volume{
				{Name: "mock"},
			},
			[]corev1.Volume{{
				Name: sidecar.UnixDomainSocketVolume,
			}, {
				Name: "mock",
			}},
			&corev1.VolumeMount{Name: sidecar.UnixDomainSocketVolume, MountPath: "/tmp"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			pod := corev1.Pod{}
			pod.Annotations = tc.annotations
			pod.Spec.Volumes = tc.originalVolumes

			socketMount := appendUnixDomainSocketVolume(&pod)

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
			pod := corev1.Pod{
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
			pod := corev1.Pod{}
			pod.Annotations = map[string]string{
				annotations.KeyVolumeMountsReadOnly:  tc.volumeReadOnlyAnnotation,
				annotations.KeyVolumeMountsReadWrite: tc.volumeReadWriteAnnotation,
			}
			pod.Spec.Volumes = []corev1.Volume{}
			for _, volumeName := range tc.podVolumeMountNames {
				pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: volumeName})
			}

			volumeMounts := getVolumeMounts(pod)
			assert.Equal(t, tc.expVolumeMounts, volumeMounts)
		})
	}
}
