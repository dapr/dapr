// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package injector

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestConfigCorrectValues(t *testing.T) {
	i := NewInjector(Config{
		TLSCertFile:            "a",
		TLSKeyFile:             "b",
		SidecarImage:           "c",
		SidecarImagePullPolicy: "d",
		Namespace:              "e",
	}, nil, nil)

	injector := i.(*injector)
	assert.Equal(t, "a", injector.config.TLSCertFile)
	assert.Equal(t, "b", injector.config.TLSKeyFile)
	assert.Equal(t, "c", injector.config.SidecarImage)
	assert.Equal(t, "d", injector.config.SidecarImagePullPolicy)
	assert.Equal(t, "e", injector.config.Namespace)
}

func TestGetConfig(t *testing.T) {
	m := map[string]string{daprConfigKey: "config1"}
	c := getConfig(m)
	assert.Equal(t, "config1", c)
}

func TestGetProfiling(t *testing.T) {
	t.Run("missing annotation", func(t *testing.T) {
		m := map[string]string{}
		e := profilingEnabled(m)
		assert.Equal(t, e, false)
	})

	t.Run("enabled", func(t *testing.T) {
		m := map[string]string{daprProfilingKey: "yes"}
		e := profilingEnabled(m)
		assert.Equal(t, e, true)
	})

	t.Run("disabled", func(t *testing.T) {
		m := map[string]string{daprProfilingKey: "false"}
		e := profilingEnabled(m)
		assert.Equal(t, e, false)
	})
	m := map[string]string{daprConfigKey: "config1"}
	c := getConfig(m)
	assert.Equal(t, "config1", c)
}

func TestGetAppPort(t *testing.T) {
	t.Run("valid port", func(t *testing.T) {
		m := map[string]string{daprPortKey: "3000"}
		p, err := getAppPort(m)
		assert.Nil(t, err)
		assert.Equal(t, int32(3000), p)
	})

	t.Run("invalid port", func(t *testing.T) {
		m := map[string]string{daprPortKey: "a"}
		p, err := getAppPort(m)
		assert.NotNil(t, err)
		assert.Equal(t, int32(-1), p)
	})
}

func TestGetProtocol(t *testing.T) {
	t.Run("valid grpc protocol", func(t *testing.T) {
		m := map[string]string{daprProtocolKey: "grpc"}
		p := getProtocol(m)
		assert.Equal(t, "grpc", p)
	})

	t.Run("valid http protocol", func(t *testing.T) {
		m := map[string]string{daprProtocolKey: "http"}
		p := getProtocol(m)
		assert.Equal(t, "http", p)
	})

	t.Run("get default http protocol", func(t *testing.T) {
		m := map[string]string{}
		p := getProtocol(m)
		assert.Equal(t, "http", p)
	})
}

func TestGetAppID(t *testing.T) {
	t.Run("get app id", func(t *testing.T) {
		m := map[string]string{appIDKey: "app"}
		pod := corev1.Pod{}
		pod.Annotations = m
		id := getAppID(pod)
		assert.Equal(t, "app", id)
	})

	t.Run("get pod id", func(t *testing.T) {
		pod := corev1.Pod{}
		pod.ObjectMeta.Name = "pod"
		id := getAppID(pod)
		assert.Equal(t, "pod", id)
	})
}

func TestLogLevel(t *testing.T) {
	t.Run("empty log level - get default", func(t *testing.T) {
		m := map[string]string{}
		logLevel := getLogLevel(m)
		assert.Equal(t, "info", logLevel)
	})

	t.Run("error log level", func(t *testing.T) {
		m := map[string]string{daprLogLevel: "error"}
		logLevel := getLogLevel(m)
		assert.Equal(t, "error", logLevel)
	})
}

func TestMaxConcurrency(t *testing.T) {
	t.Run("empty max concurrency - should be -1", func(t *testing.T) {
		m := map[string]string{}
		maxConcurrency, err := getMaxConcurrency(m)
		assert.Nil(t, err)
		assert.Equal(t, int32(-1), maxConcurrency)
	})

	t.Run("invalid max concurrency - should be -1", func(t *testing.T) {
		m := map[string]string{daprMaxConcurrencyKey: "invalid"}
		_, err := getMaxConcurrency(m)
		assert.NotNil(t, err)
	})

	t.Run("valid max concurrency - should be 10", func(t *testing.T) {
		m := map[string]string{daprMaxConcurrencyKey: "10"}
		maxConcurrency, err := getMaxConcurrency(m)
		assert.Nil(t, err)
		assert.Equal(t, int32(10), maxConcurrency)
	})
}

func TestKubernetesDNS(t *testing.T) {
	dns := getKubernetesDNS("a", "b")
	assert.Equal(t, "a.b.svc.cluster.local", dns)
}

func TestGetMetricsPort(t *testing.T) {
	t.Run("metrics port override", func(t *testing.T) {
		m := map[string]string{daprMetricsPortKey: "5050"}
		pod := corev1.Pod{}
		pod.Annotations = m
		p := getMetricsPort(pod.Annotations)
		assert.Equal(t, 5050, p)
	})
	t.Run("invalid metrics port override", func(t *testing.T) {
		m := map[string]string{daprMetricsPortKey: "abc"}
		pod := corev1.Pod{}
		pod.Annotations = m
		p := getMetricsPort(pod.Annotations)
		assert.Equal(t, defaultMetricsPort, p)
	})
	t.Run("no metrics port defined", func(t *testing.T) {
		pod := corev1.Pod{}
		p := getMetricsPort(pod.Annotations)
		assert.Equal(t, defaultMetricsPort, p)
	})
}

func TestGetContainer(t *testing.T) {
	c := getSidecarContainer("5000", "http", "app", "config1", "image", "ns", "a", "b", false, "info", true, "-1", nil, "", "", "", "", false, "", 9090, nil)
	assert.NotNil(t, c)
	assert.Equal(t, "image", c.Image)
}

func TestSidecarResourceLimits(t *testing.T) {
	t.Run("with limits", func(t *testing.T) {
		r := &v1.ResourceRequirements{
			Limits: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("100m"),
				v1.ResourceMemory: resource.MustParse("1Gi"),
			},
		}

		c := getSidecarContainer("5000", "http", "app", "config1", "image", "ns", "a", "b", false, "info", true, "-1", nil, "", "", "", "", false, "", 9090, r)
		assert.NotNil(t, c)
		assert.Equal(t, "100m", c.Resources.Limits.Cpu().String())
		assert.Equal(t, "1Gi", c.Resources.Limits.Memory().String())
	})

	t.Run("with requests", func(t *testing.T) {
		r := &v1.ResourceRequirements{
			Requests: v1.ResourceList{
				v1.ResourceCPU:    resource.MustParse("100m"),
				v1.ResourceMemory: resource.MustParse("1Gi"),
			},
		}

		c := getSidecarContainer("5000", "http", "app", "config1", "image", "ns", "a", "b", false, "info", true, "-1", nil, "", "", "", "", false, "", 9090, r)
		assert.NotNil(t, c)
		assert.Equal(t, "100m", c.Resources.Requests.Cpu().String())
		assert.Equal(t, "1Gi", c.Resources.Requests.Memory().String())
	})

	t.Run("no limits", func(t *testing.T) {
		c := getSidecarContainer("5000", "http", "app", "config1", "image", "ns", "a", "b", false, "info", true, "-1", nil, "", "", "", "", false, "", 9090, nil)
		assert.NotNil(t, c)
		assert.Len(t, c.Resources.Limits, 0)
	})
}

func TestGetAppIDFromRequest(t *testing.T) {
	t.Run("can handle nil", func(t *testing.T) {
		appID := getAppIDFromRequest(nil)
		assert.Equal(t, "", appID)
	})

	t.Run("can handle empty admissionrequest object", func(t *testing.T) {
		fakeReq := &v1beta1.AdmissionRequest{}
		appID := getAppIDFromRequest(fakeReq)
		assert.Equal(t, "", appID)
	})

	t.Run("can get correct appID", func(t *testing.T) {
		fakePod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"dapr.io/id": "fakeID",
				},
			},
		}
		rawBytes, _ := json.Marshal(fakePod)
		fakeReq := &v1beta1.AdmissionRequest{
			Object: runtime.RawExtension{
				Raw: rawBytes,
			},
		}
		appID := getAppIDFromRequest(fakeReq)
		assert.Equal(t, "fakeID", appID)
	})
}

func TestGetResourceRequirements(t *testing.T) {
	t.Run("no resource requirements", func(t *testing.T) {
		r, err := getResourceRequirements(nil)
		assert.Nil(t, err)
		assert.Nil(t, r)
	})

	t.Run("valid resource limits", func(t *testing.T) {
		a := map[string]string{daprCPULimitKey: "100m", daprMemoryLimitKey: "1Gi"}
		r, err := getResourceRequirements(a)
		assert.Nil(t, err)
		assert.Equal(t, "100m", r.Limits.Cpu().String())
		assert.Equal(t, "1Gi", r.Limits.Memory().String())
	})

	t.Run("invalid cpu limit", func(t *testing.T) {
		a := map[string]string{daprCPULimitKey: "cpu", daprMemoryLimitKey: "1Gi"}
		r, err := getResourceRequirements(a)
		assert.NotNil(t, err)
		assert.Nil(t, r)
	})

	t.Run("invalid memory limit", func(t *testing.T) {
		a := map[string]string{daprCPULimitKey: "100m", daprMemoryLimitKey: "memory"}
		r, err := getResourceRequirements(a)
		assert.NotNil(t, err)
		assert.Nil(t, r)
	})

	t.Run("valid resource requests", func(t *testing.T) {
		a := map[string]string{daprCPURequestKey: "100m", daprMemoryRequestKey: "1Gi"}
		r, err := getResourceRequirements(a)
		assert.Nil(t, err)
		assert.Equal(t, "100m", r.Requests.Cpu().String())
		assert.Equal(t, "1Gi", r.Requests.Memory().String())
	})

	t.Run("invalid cpu request", func(t *testing.T) {
		a := map[string]string{daprCPURequestKey: "cpu", daprMemoryRequestKey: "1Gi"}
		r, err := getResourceRequirements(a)
		assert.NotNil(t, err)
		assert.Nil(t, r)
	})

	t.Run("invalid memory request", func(t *testing.T) {
		a := map[string]string{daprCPURequestKey: "100m", daprMemoryRequestKey: "memory"}
		r, err := getResourceRequirements(a)
		assert.NotNil(t, err)
		assert.Nil(t, r)
	})
}
