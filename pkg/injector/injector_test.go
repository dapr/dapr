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
	"github.com/dapr/dapr/pkg/injector/annotations"
	"github.com/dapr/dapr/pkg/injector/sidecar"
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

func TestAnnotations(t *testing.T) {
	t.Run("Config", func(t *testing.T) {
		m := map[string]string{annotations.KeyConfig: "config1"}
		an := annotations.New(m)
		assert.Equal(t, "config1", an.GetString(annotations.KeyConfig))
	})

	t.Run("Profiling", func(t *testing.T) {
		t.Run("missing annotation", func(t *testing.T) {
			m := map[string]string{}
			an := annotations.New(m)
			e := an.GetBoolOrDefault(annotations.KeyEnableProfiling, annotations.DefaultEnableProfiling)
			assert.Equal(t, e, false)
		})

		t.Run("enabled", func(t *testing.T) {
			m := map[string]string{annotations.KeyEnableProfiling: "yes"}
			an := annotations.New(m)
			e := an.GetBoolOrDefault(annotations.KeyEnableProfiling, annotations.DefaultEnableProfiling)
			assert.Equal(t, e, true)
		})

		t.Run("disabled", func(t *testing.T) {
			m := map[string]string{annotations.KeyEnableProfiling: "false"}
			an := annotations.New(m)
			e := an.GetBoolOrDefault(annotations.KeyEnableProfiling, annotations.DefaultEnableProfiling)
			assert.Equal(t, e, false)
		})
	})

	t.Run("AppPort", func(t *testing.T) {
		t.Run("valid port", func(t *testing.T) {
			m := map[string]string{annotations.KeyAppPort: "3000"}
			an := annotations.New(m)
			p, err := an.GetInt32(annotations.KeyAppPort)
			assert.Nil(t, err)
			assert.Equal(t, int32(3000), p)
		})

		t.Run("invalid port", func(t *testing.T) {
			m := map[string]string{annotations.KeyAppPort: "a"}
			an := annotations.New(m)
			p, err := an.GetInt32(annotations.KeyAppPort)
			assert.NotNil(t, err)
			assert.Equal(t, int32(-1), p)
		})
	})

	t.Run("Protocol", func(t *testing.T) {
		t.Run("valid grpc protocol", func(t *testing.T) {
			m := map[string]string{annotations.KeyAppProtocol: "grpc"}
			an := annotations.New(m)
			p := an.GetStringOrDefault(annotations.KeyAppProtocol, annotations.DefaultAppProtocol)
			assert.Equal(t, "grpc", p)
		})

		t.Run("valid http protocol", func(t *testing.T) {
			m := map[string]string{annotations.KeyAppProtocol: "http"}
			an := annotations.New(m)
			p := an.GetStringOrDefault(annotations.KeyAppProtocol, annotations.DefaultAppProtocol)
			assert.Equal(t, "http", p)
		})

		t.Run("get default http protocol", func(t *testing.T) {
			m := map[string]string{}
			an := annotations.New(m)
			p := an.GetStringOrDefault(annotations.KeyAppProtocol, annotations.DefaultAppProtocol)
			assert.Equal(t, "http", p)
		})
	})

	t.Run("LogLevel", func(t *testing.T) {
		t.Run("empty log level - get default", func(t *testing.T) {
			m := map[string]string{}
			an := annotations.New(m)
			logLevel := an.GetStringOrDefault(annotations.KeyLogLevel, annotations.DefaultLogLevel)
			assert.Equal(t, "info", logLevel)
		})

		t.Run("error log level", func(t *testing.T) {
			m := map[string]string{annotations.KeyLogLevel: "error"}
			an := annotations.New(m)
			logLevel := an.GetStringOrDefault(annotations.KeyLogLevel, annotations.DefaultLogLevel)
			assert.Equal(t, "error", logLevel)
		})
	})

	t.Run("MaxConcurrency", func(t *testing.T) {
		t.Run("empty max concurrency - should be -1", func(t *testing.T) {
			m := map[string]string{}
			an := annotations.New(m)
			maxConcurrency, err := an.GetInt32(annotations.KeyAppMaxConcurrency)
			assert.Nil(t, err)
			assert.Equal(t, int32(-1), maxConcurrency)
		})

		t.Run("invalid max concurrency - should be -1", func(t *testing.T) {
			m := map[string]string{annotations.KeyAppMaxConcurrency: "invalid"}
			an := annotations.New(m)
			_, err := an.GetInt32(annotations.KeyAppMaxConcurrency)
			assert.NotNil(t, err)
		})

		t.Run("valid max concurrency - should be 10", func(t *testing.T) {
			m := map[string]string{annotations.KeyAppMaxConcurrency: "10"}
			an := annotations.New(m)
			maxConcurrency, err := an.GetInt32(annotations.KeyAppMaxConcurrency)
			assert.Nil(t, err)
			assert.Equal(t, int32(10), maxConcurrency)
		})
	})

	t.Run("GetMetricsPort", func(t *testing.T) {
		t.Run("metrics port override", func(t *testing.T) {
			m := map[string]string{annotations.KeyMetricsPort: "5050"}
			an := annotations.New(m)
			pod := corev1.Pod{}
			pod.Annotations = m
			p := an.GetInt32OrDefault(annotations.KeyMetricsPort, annotations.DefaultMetricsPort)
			assert.Equal(t, int32(5050), p)
		})
		t.Run("invalid metrics port override", func(t *testing.T) {
			m := map[string]string{annotations.KeyMetricsPort: "abc"}
			an := annotations.New(m)
			pod := corev1.Pod{}
			pod.Annotations = m
			p := an.GetInt32OrDefault(annotations.KeyMetricsPort, annotations.DefaultMetricsPort)
			assert.Equal(t, int32(annotations.DefaultMetricsPort), p)
		})
		t.Run("no metrics port defined", func(t *testing.T) {
			pod := corev1.Pod{}
			an := annotations.New(pod.Annotations)
			p := an.GetInt32OrDefault(annotations.KeyMetricsPort, annotations.DefaultMetricsPort)
			assert.Equal(t, int32(annotations.DefaultMetricsPort), p)
		})
	})

	t.Run("GetSidecarContainer", func(t *testing.T) {
		c, _ := sidecar.GetSidecarContainer(sidecar.ContainerConfig{
			DaprSidecarImage: "image",
		})

		assert.NotNil(t, c)
		assert.Equal(t, "image", c.Image)
	})

	t.Run("SidecarResourceLimits", func(t *testing.T) {
		t.Run("with limits", func(t *testing.T) {
			an := map[string]string{
				annotations.KeyCPULimit:    "100m",
				annotations.KeyMemoryLimit: "1Gi",
			}
			c, _ := sidecar.GetSidecarContainer(sidecar.ContainerConfig{
				Annotations: an,
			})
			assert.NotNil(t, c)
			assert.Len(t, c.Resources.Requests, 0)
			assert.Equal(t, "100m", c.Resources.Limits.Cpu().String())
			assert.Equal(t, "1Gi", c.Resources.Limits.Memory().String())
		})

		t.Run("with requests", func(t *testing.T) {
			an := map[string]string{
				annotations.KeyCPURequest:    "100m",
				annotations.KeyMemoryRequest: "1Gi",
			}
			c, _ := sidecar.GetSidecarContainer(sidecar.ContainerConfig{
				Annotations: an,
			})
			assert.NotNil(t, c)
			assert.Len(t, c.Resources.Limits, 0)
			assert.Equal(t, "100m", c.Resources.Requests.Cpu().String())
			assert.Equal(t, "1Gi", c.Resources.Requests.Memory().String())
		})

		t.Run("no limits", func(t *testing.T) {
			c, _ := sidecar.GetSidecarContainer(sidecar.ContainerConfig{})
			assert.NotNil(t, c)
			assert.Len(t, c.Resources.Limits, 0)
		})
	})

	t.Run("AppSSL", func(t *testing.T) {
		t.Run("ssl enabled", func(t *testing.T) {
			an := annotations.New(map[string]string{
				annotations.KeyAppSSL: "true",
			})
			s := an.GetBoolOrDefault(annotations.KeyAppSSL, annotations.DefaultAppSSL)
			assert.True(t, s)
		})

		t.Run("ssl disabled", func(t *testing.T) {
			an := annotations.New(map[string]string{
				annotations.KeyAppSSL: "false",
			})
			s := an.GetBoolOrDefault(annotations.KeyAppSSL, annotations.DefaultAppSSL)
			assert.False(t, s)
		})

		t.Run("ssl not specified", func(t *testing.T) {
			an := annotations.New(map[string]string{})
			s := an.GetBoolOrDefault(annotations.KeyAppSSL, annotations.DefaultAppSSL)
			assert.False(t, s)
		})

		t.Run("get sidecar container enabled", func(t *testing.T) {
			an := map[string]string{
				annotations.KeyAppSSL: "true",
			}
			c, _ := sidecar.GetSidecarContainer(sidecar.ContainerConfig{
				Annotations: an,
			})
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
			an := map[string]string{
				annotations.KeyAppSSL: "false",
			}
			c, _ := sidecar.GetSidecarContainer(sidecar.ContainerConfig{
				Annotations: an,
			})
			for _, a := range c.Args {
				if a == "--app-ssl" {
					t.FailNow()
				}
			}
		})

		t.Run("get sidecar container not specified", func(t *testing.T) {
			c, _ := sidecar.GetSidecarContainer(sidecar.ContainerConfig{})
			for _, a := range c.Args {
				if a == "--app-ssl" {
					t.FailNow()
				}
			}
		})
	})
}

func TestSidecarContainerVolumeMounts(t *testing.T) {
	t.Run("sidecar contains custom volume mounts", func(t *testing.T) {
		volumeMounts := []corev1.VolumeMount{
			{Name: "foo", MountPath: "/foo"},
			{Name: "bar", MountPath: "/bar"},
		}

		c, _ := sidecar.GetSidecarContainer(sidecar.ContainerConfig{
			VolumeMounts: volumeMounts,
		})
		assert.Equal(t, 2, len(c.VolumeMounts))
		assert.Equal(t, volumeMounts[0], c.VolumeMounts[0])
		assert.Equal(t, volumeMounts[1], c.VolumeMounts[1])
	})

	t.Run("sidecar contains all volume mounts", func(t *testing.T) {
		socketVolumeMount := &corev1.VolumeMount{
			Name:      "socket-mount",
			MountPath: "/socket/mount",
		}
		tokenVolumeMount := &corev1.VolumeMount{
			Name:      "token-mount",
			MountPath: "/token/mount",
		}
		volumeMounts := []corev1.VolumeMount{
			*socketVolumeMount,
			*tokenVolumeMount,
			{Name: "foo", MountPath: "/foo"},
			{Name: "bar", MountPath: "/bar"},
		}

		c, _ := sidecar.GetSidecarContainer(sidecar.ContainerConfig{
			VolumeMounts: volumeMounts,
		})
		assert.Equal(t, 4, len(c.VolumeMounts))
		assert.Equal(t, *socketVolumeMount, c.VolumeMounts[0])
		assert.Equal(t, *tokenVolumeMount, c.VolumeMounts[1])
		assert.Equal(t, volumeMounts[2], c.VolumeMounts[2])
		assert.Equal(t, volumeMounts[3], c.VolumeMounts[3])
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

	t.Run("injector config has no allowed service account", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(context.TODO(), Config{}, client)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(uids))
	})

	t.Run("injector config has a valid allowed service account", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(context.TODO(), Config{AllowedServiceAccounts: "test:test"}, client)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(uids))
	})

	t.Run("injector config has a invalid allowed service account", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(context.TODO(), Config{AllowedServiceAccounts: "abc:abc"}, client)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(uids))
	})

	t.Run("injector config has multiple allowed service accounts", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(context.TODO(), Config{AllowedServiceAccounts: "test:test,abc:abc"}, client)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(uids))
	})
}
