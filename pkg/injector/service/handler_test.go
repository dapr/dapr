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

package service

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
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/dapr/dapr/pkg/client/clientset/versioned/fake"
)

func TestHandleRequest(t *testing.T) {
	authID := "test-auth-id"

	i, err := NewInjector(Options{
		AuthUIDs: []string{authID},
		Config: Config{
			SidecarImage:                      "test-image",
			Namespace:                         "test-ns",
			ControlPlaneTrustDomain:           "test-trust-domain",
			AllowedServiceAccountsPrefixNames: "vc-proj*:sa-dev*,vc-all-allowed*:*",
		},
		DaprClient: fake.NewSimpleClientset(),
		KubeClient: kubernetesfake.NewSimpleClientset(),
	})

	require.NoError(t, err)
	injector := i.(*injector)
	injector.currentTrustAnchors = func() ([]byte, error) {
		return nil, nil
	}
	injector.signDaprdCertificate = func(context.Context, string) ([]byte, []byte, error) {
		return []byte("test-cert"), []byte("test-key"), nil
	}

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
		request          admissionv1.AdmissionReview
		contentType      string
		expectStatusCode int
		expectPatched    bool
	}{
		{
			"TestSidecarInjectSuccess",
			admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
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
			admissionv1.AdmissionReview{},
			runtime.ContentTypeYAML,
			http.StatusUnsupportedMediaType,
			true,
		},
		{
			"TestSidecarInjectInvalidKind",
			admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
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
			admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
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
			admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
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
			admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
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
			admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
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
		{
			"TestSidecarInjectUserInfoMatchesServiceAccountPrefix",
			admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Name:      "test-app",
					Namespace: "test-ns",
					Operation: "CREATE",
					UserInfo: authenticationv1.UserInfo{
						Username: "system:serviceaccount:vc-project-star:sa-dev-team-usa",
					},
					Object: runtime.RawExtension{Raw: podBytes},
				},
			},
			runtime.ContentTypeJSON,
			http.StatusOK,
			true,
		},
		{
			"TestSidecarInjectUserInfoMatchesAllAllowedServiceAccountPrefix",
			admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Name:      "test-app",
					Namespace: "test-ns",
					Operation: "CREATE",
					UserInfo: authenticationv1.UserInfo{
						Username: "system:serviceaccount:vc-all-allowed-project:dapr",
					},
					Object: runtime.RawExtension{Raw: podBytes},
				},
			},
			runtime.ContentTypeJSON,
			http.StatusOK,
			true,
		},
		{
			"TestSidecarInjectUserInfoNotMatchesServiceAccountPrefix",
			admissionv1.AdmissionReview{
				Request: &admissionv1.AdmissionRequest{
					UID:       uuid.NewUUID(),
					Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
					Name:      "test-app",
					Namespace: "test-ns",
					Operation: "CREATE",
					UserInfo: authenticationv1.UserInfo{
						Username: "system:serviceaccount:vc-bad-project-star:sa-dev-team-usa",
					},
					Object: runtime.RawExtension{Raw: podBytes},
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
			require.NoError(t, err)

			resp, err := http.Post(ts.URL, tc.contentType, bytes.NewBuffer(requestBytes))
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.expectStatusCode, resp.StatusCode)

			if resp.StatusCode == http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)

				var ar admissionv1.AdmissionReview
				err = json.Unmarshal(body, &ar)
				require.NoError(t, err)

				assert.Equal(t, tc.expectPatched, len(ar.Response.Patch) > 0)
			}
		})
	}
}
