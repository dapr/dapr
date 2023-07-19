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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/dapr/dapr/pkg/injector/annotations"
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
			c := NewSidecarConfig(&corev1.Pod{})
			c.Env = tc.envStr
			envKeys, envVars := c.GetEnv()
			assert.Equal(t, tc.expLen, len(envVars))
			assert.Equal(t, tc.expKeys, envKeys)
			assert.Equal(t, tc.expEnv, envVars)
		})
	}
}

func TestGetResourceRequirements(t *testing.T) {
	t.Run("no resource requirements", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.NoError(t, err)
		assert.Nil(t, r)
	})

	t.Run("valid resource limits", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPULimit:    "100m",
					annotations.KeyMemoryLimit: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.NoError(t, err)
		assert.Equal(t, "100m", r.Limits.Cpu().String())
		assert.Equal(t, "1Gi", r.Limits.Memory().String())
	})

	t.Run("invalid cpu limit", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPULimit:    "invalid",
					annotations.KeyMemoryLimit: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("invalid memory limit", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPULimit:    "100m",
					annotations.KeyMemoryLimit: "invalid",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("valid resource requests", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPURequest:    "100m",
					annotations.KeyMemoryRequest: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.NoError(t, err)
		assert.Equal(t, "100m", r.Requests.Cpu().String())
		assert.Equal(t, "1Gi", r.Requests.Memory().String())
	})

	t.Run("invalid cpu request", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPURequest:    "invalid",
					annotations.KeyMemoryRequest: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("invalid memory request", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPURequest:    "100m",
					annotations.KeyMemoryRequest: "invalid",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.Error(t, err)
		assert.Nil(t, r)
	})

	t.Run("limits and requests", func(t *testing.T) {
		c := NewSidecarConfig(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annotations.KeyCPULimit:      "200m",
					annotations.KeyMemoryLimit:   "2Gi",
					annotations.KeyCPURequest:    "100m",
					annotations.KeyMemoryRequest: "1Gi",
				},
			},
		})
		c.SetFromPodAnnotations()
		r, err := c.getResourceRequirements()
		require.NoError(t, err)
		assert.Equal(t, "200m", r.Limits.Cpu().String())
		assert.Equal(t, "2Gi", r.Limits.Memory().String())
		assert.Equal(t, "100m", r.Requests.Cpu().String())
		assert.Equal(t, "1Gi", r.Requests.Memory().String())
	})
}

func TestGetProbeHttpHandler(t *testing.T) {
	pathElements := []string{"api", "v1", "healthz"}
	expectedPath := "/api/v1/healthz"
	expectedHandler := corev1.ProbeHandler{
		HTTPGet: &corev1.HTTPGetAction{
			Path: expectedPath,
			Port: intstr.IntOrString{IntVal: 3500},
		},
	}

	assert.EqualValues(t, expectedHandler, getProbeHTTPHandler(3500, pathElements...))
}

func TestFormatProbePath(t *testing.T) {
	testCases := []struct {
		given    []string
		expected string
	}{
		{
			given:    []string{"api", "v1"},
			expected: "/api/v1",
		},
		{
			given:    []string{"//api", "v1"},
			expected: "/api/v1",
		},
		{
			given:    []string{"//api", "/v1/"},
			expected: "/api/v1",
		},
		{
			given:    []string{"//api", "/v1/", "healthz"},
			expected: "/api/v1/healthz",
		},
		{
			given:    []string{""},
			expected: "/",
		},
	}

	for _, tc := range testCases {
		assert.Equal(t, tc.expected, formatProbePath(tc.given...))
	}
}
