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

package injector

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/dapr/dapr/pkg/client/clientset/versioned/fake"
)

const (
	appPort = "5000"
)

func TestConfigCorrectValues(t *testing.T) {
	i := NewInjector(nil, Config{
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
		m := map[string]string{daprEnableProfilingKey: "yes"}
		e := profilingEnabled(m)
		assert.Equal(t, e, true)
	})

	t.Run("disabled", func(t *testing.T) {
		m := map[string]string{daprEnableProfilingKey: "false"}
		e := profilingEnabled(m)
		assert.Equal(t, e, false)
	})
	m := map[string]string{daprConfigKey: "config1"}
	c := getConfig(m)
	assert.Equal(t, "config1", c)
}

func TestGetAppPort(t *testing.T) {
	t.Run("valid port", func(t *testing.T) {
		m := map[string]string{daprAppPortKey: "3000"}
		p, err := getAppPort(m)
		assert.Nil(t, err)
		assert.Equal(t, int32(3000), p)
	})

	t.Run("invalid port", func(t *testing.T) {
		m := map[string]string{daprAppPortKey: "a"}
		p, err := getAppPort(m)
		assert.NotNil(t, err)
		assert.Equal(t, int32(-1), p)
	})
}

func TestGetProtocol(t *testing.T) {
	t.Run("valid grpc protocol", func(t *testing.T) {
		m := map[string]string{daprAppProtocolKey: "grpc"}
		p := getProtocol(m)
		assert.Equal(t, "grpc", p)
	})

	t.Run("valid http protocol", func(t *testing.T) {
		m := map[string]string{daprAppProtocolKey: "http"}
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
		m := map[string]string{daprAppMaxConcurrencyKey: "invalid"}
		_, err := getMaxConcurrency(m)
		assert.NotNil(t, err)
	})

	t.Run("valid max concurrency - should be 10", func(t *testing.T) {
		m := map[string]string{daprAppMaxConcurrencyKey: "10"}
		maxConcurrency, err := getMaxConcurrency(m)
		assert.Nil(t, err)
		assert.Equal(t, int32(10), maxConcurrency)
	})
}

func TestGetServiceAddress(t *testing.T) {
	testCases := []struct {
		name          string
		namespace     string
		clusterDomain string
		port          int
		expect        string
	}{
		{
			port:          80,
			name:          "a",
			namespace:     "b",
			clusterDomain: "cluster.local",
			expect:        "a.b.svc.cluster.local:80",
		},
		{
			port:          50001,
			name:          "app",
			namespace:     "default",
			clusterDomain: "selfdefine.domain",
			expect:        "app.default.svc.selfdefine.domain:50001",
		},
	}
	for _, tc := range testCases {
		dns := getServiceAddress(tc.name, tc.namespace, tc.clusterDomain, tc.port)
		assert.Equal(t, tc.expect, dns)
	}
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
	annotations := map[string]string{}
	annotations[daprConfigKey] = "config"
	annotations[daprAppPortKey] = appPort

	c, _ := getSidecarContainer(annotations, "app", "image", "Always", "ns", "a", "b", nil, nil, nil, "", "", "", "", false, "")

	assert.NotNil(t, c)
	assert.Equal(t, "image", c.Image)
}

func TestSidecarResourceLimits(t *testing.T) {
	t.Run("with limits", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = "config1"
		annotations[daprAppPortKey] = appPort
		annotations[daprLogAsJSON] = "true" //nolint:goconst
		annotations[daprCPULimitKey] = "100m"
		annotations[daprMemoryLimitKey] = "1Gi"

		c, _ := getSidecarContainer(annotations, "app", "image", "Always", "ns", "a", "b", nil, nil, nil, "", "", "", "", false, "")
		assert.NotNil(t, c)
		assert.Equal(t, "100m", c.Resources.Limits.Cpu().String())
		assert.Equal(t, "1Gi", c.Resources.Limits.Memory().String())
	})

	t.Run("with requests", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = "config1"
		annotations[daprAppPortKey] = appPort
		annotations[daprLogAsJSON] = "true"
		annotations[daprCPURequestKey] = "100m"
		annotations[daprMemoryRequestKey] = "1Gi"

		c, _ := getSidecarContainer(annotations, "app", "image", "Always", "ns", "a", "b", nil, nil, nil, "", "", "", "", false, "")
		assert.NotNil(t, c)
		assert.Equal(t, "100m", c.Resources.Requests.Cpu().String())
		assert.Equal(t, "1Gi", c.Resources.Requests.Memory().String())
	})

	t.Run("no limits", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprConfigKey] = "config1"
		annotations[daprAppPortKey] = appPort
		annotations[daprLogAsJSON] = "true"

		c, _ := getSidecarContainer(annotations, "app", "image", "Always", "ns", "a", "b", nil, nil, nil, "", "", "", "", false, "")
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
		fakeReq := &v1.AdmissionRequest{}
		appID := getAppIDFromRequest(fakeReq)
		assert.Equal(t, "", appID)
	})

	t.Run("can get correct appID", func(t *testing.T) {
		fakePod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"dapr.io/app-id": "fakeID",
				},
			},
		}
		rawBytes, _ := json.Marshal(fakePod)
		fakeReq := &v1.AdmissionRequest{
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

func TestAPITokenSecret(t *testing.T) {
	t.Run("secret exists", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprAPITokenSecret] = "secret"

		s := getAPITokenSecret(annotations)
		assert.NotNil(t, s)
	})

	t.Run("secret empty", func(t *testing.T) {
		annotations := map[string]string{}
		annotations[daprAPITokenSecret] = ""

		s := getAPITokenSecret(annotations)
		assert.Equal(t, "", s)
	})
}

func TestAppSSL(t *testing.T) {
	t.Run("ssl enabled", func(t *testing.T) {
		annotations := map[string]string{
			daprAppSSLKey: "true",
		}
		s := appSSLEnabled(annotations)
		assert.True(t, s)
	})

	t.Run("ssl disabled", func(t *testing.T) {
		annotations := map[string]string{
			daprAppSSLKey: "false",
		}
		s := appSSLEnabled(annotations)
		assert.False(t, s)
	})

	t.Run("ssl not specified", func(t *testing.T) {
		annotations := map[string]string{}
		s := appSSLEnabled(annotations)
		assert.False(t, s)
	})

	t.Run("get sidecar container enabled", func(t *testing.T) {
		annotations := map[string]string{
			daprAppSSLKey: "true",
		}
		c, _ := getSidecarContainer(annotations, "app", "image", "", "ns", "a", "b", nil, nil, nil, "", "", "", "", false, "")
		found := false
		for _, a := range c.Args {
			if a == "--app-ssl" {
				found = true
				break
			}
		}
		assert.True(t, found)
	})

	t.Run("get sidecar container disabled", func(t *testing.T) {
		annotations := map[string]string{
			daprAppSSLKey: "false",
		}
		c, _ := getSidecarContainer(annotations, "app", "image", "Always", "ns", "a", "b", nil, nil, nil, "", "", "", "", false, "")
		for _, a := range c.Args {
			if a == "--app-ssl" {
				t.FailNow()
			}
		}
	})

	t.Run("get sidecar container not specified", func(t *testing.T) {
		annotations := map[string]string{}
		c, _ := getSidecarContainer(annotations, "app", "image", "Always", "ns", "a", "b", nil, nil, nil, "", "", "", "", false, "")
		for _, a := range c.Args {
			if a == "--app-ssl" {
				t.FailNow()
			}
		}
	})
}

func TestSidecarContainerVolumeMounts(t *testing.T) {
	t.Run("sidecar contains custom volume mounts", func(t *testing.T) {
		annotations := map[string]string{}
		volumeMounts := []corev1.VolumeMount{
			{Name: "foo", MountPath: "/foo"},
			{Name: "bar", MountPath: "/bar"},
		}

		c, _ := getSidecarContainer(annotations, "app", "image", "Always", "ns", "a", "b", nil, nil, volumeMounts, "", "", "", "", false, "")
		assert.Equal(t, 2, len(c.VolumeMounts))
		assert.Equal(t, volumeMounts[0], c.VolumeMounts[0])
		assert.Equal(t, volumeMounts[1], c.VolumeMounts[1])
	})

	t.Run("sidecar contains all volume mounts", func(t *testing.T) {
		annotations := map[string]string{}
		socketVolumeMount := corev1.VolumeMount{
			Name:      "socket-mount",
			MountPath: "/socket/mount",
		}
		tokenVolumeMount := corev1.VolumeMount{
			Name:      "token-mount",
			MountPath: "/token/mount",
		}
		volumeMounts := []corev1.VolumeMount{
			{Name: "foo", MountPath: "/foo"},
			{Name: "bar", MountPath: "/bar"},
		}

		c, _ := getSidecarContainer(annotations, "app", "image", "Always", "ns", "a", "b", &socketVolumeMount, &tokenVolumeMount, volumeMounts, "", "", "", "", false, "")
		assert.Equal(t, 4, len(c.VolumeMounts))
		assert.Equal(t, socketVolumeMount, c.VolumeMounts[0])
		assert.Equal(t, tokenVolumeMount, c.VolumeMounts[1])
		assert.Equal(t, volumeMounts[0], c.VolumeMounts[2])
		assert.Equal(t, volumeMounts[1], c.VolumeMounts[3])
	})
}

func TestHandleRequest(t *testing.T) {
	authID := "test-auth-id"

	i := NewInjector([]string{authID}, Config{
		TLSCertFile:  "test-cert",
		TLSKeyFile:   "test-key",
		SidecarImage: "test-image",
		Namespace:    "test-ns",
	}, fake.NewSimpleClientset(), kubernetesfake.NewSimpleClientset())
	injector := i.(*injector)

	podBytes, _ := json.Marshal(corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-app",
			Namespace:         "default",
			CreationTimestamp: metav1.Time{Time: time.Now()},
			Labels: map[string]string{
				"app": "test-app",
			},
			Annotations: map[string]string{
				"dapr.io/enabled":  "true",
				"dapr.io/app-id":   "test-app",
				"dapr.io/app-port": "3000",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "docker.io/app:latest",
				},
			},
		},
	})

	testCases := []struct {
		testName         string
		request          v1.AdmissionReview
		contentType      string
		expectStatusCode int
		expectPatched    bool
	}{
		{
			"TestSidecarInjectSuccess",
			v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Name:      "test-app",
					Namespace: "test-ns",
					Operation: "CREATE",
					UserInfo: authenticationv1.UserInfo{
						Groups: []string{systemGroup},
					},
					Object: runtime.RawExtension{Raw: podBytes},
				},
			},
			runtime.ContentTypeJSON,
			http.StatusOK,
			true,
		},
		{
			"TestSidecarInjectWrongContentType",
			v1.AdmissionReview{},
			runtime.ContentTypeYAML,
			http.StatusUnsupportedMediaType,
			true,
		},
		{
			"TestSidecarInjectInvalidKind",
			v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Deployment"},
					Name:      "test-app",
					Namespace: "test-ns",
					Operation: "CREATE",
					UserInfo: authenticationv1.UserInfo{
						Groups: []string{systemGroup},
					},
					Object: runtime.RawExtension{Raw: podBytes},
				},
			},
			runtime.ContentTypeJSON,
			http.StatusOK,
			false,
		},
		{
			"TestSidecarInjectGroupsNotContains",
			v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Name:      "test-app",
					Namespace: "test-ns",
					Operation: "CREATE",
					UserInfo: authenticationv1.UserInfo{
						Groups: []string{"system:kubelet"},
					},
					Object: runtime.RawExtension{Raw: podBytes},
				},
			},
			runtime.ContentTypeJSON,
			http.StatusOK,
			false,
		},
		{
			"TestSidecarInjectUIDContains",
			v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Name:      "test-app",
					Namespace: "test-ns",
					Operation: "CREATE",
					UserInfo: authenticationv1.UserInfo{
						UID: authID,
					},
					Object: runtime.RawExtension{Raw: podBytes},
				},
			},
			runtime.ContentTypeJSON,
			http.StatusOK,
			true,
		},
		{
			"TestSidecarInjectUIDNotContains",
			v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Name:      "test-app",
					Namespace: "test-ns",
					Operation: "CREATE",
					UserInfo: authenticationv1.UserInfo{
						UID: "auth-id-123",
					},
					Object: runtime.RawExtension{Raw: podBytes},
				},
			},
			runtime.ContentTypeJSON,
			http.StatusOK,
			false,
		},
		{
			"TestSidecarInjectEmptyPod",
			v1.AdmissionReview{
				Request: &v1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Name:      "test-app",
					Namespace: "test-ns",
					Operation: "CREATE",
					UserInfo: authenticationv1.UserInfo{
						Groups: []string{systemGroup},
					},
					Object: runtime.RawExtension{Raw: nil},
				},
			},
			runtime.ContentTypeJSON,
			http.StatusOK,
			false,
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(injector.handleRequest))
	defer ts.Close()

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			requestBytes, err := json.Marshal(tc.request)
			assert.NoError(t, err)

			resp, err := http.Post(ts.URL, tc.contentType, bytes.NewBuffer(requestBytes))
			assert.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.expectStatusCode, resp.StatusCode)

			if resp.StatusCode == http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				assert.NoError(t, err)

				var ar v1.AdmissionReview
				err = json.Unmarshal(body, &ar)
				assert.NoError(t, err)

				assert.Equal(t, tc.expectPatched, len(ar.Response.Patch) > 0)
			}
		})
	}
}

func TestAllowedControllersServiceAccountUID(t *testing.T) {
	client := kubernetesfake.NewSimpleClientset()

	testCases := []struct {
		name      string
		namespace string
	}{
		{"replicaset-controller", metav1.NamespaceSystem},
		{"tekton-pipelines-controller", "tekton-pipelines"},
		{"test", "test"},
	}

	for _, testCase := range testCases {
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testCase.name,
				Namespace: testCase.namespace,
			},
		}
		_, err := client.CoreV1().ServiceAccounts(testCase.namespace).Create(context.TODO(), sa, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	uids, err := getServiceAccount(context.TODO(), client, AllowedServiceAccountInfos)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(uids))
}
