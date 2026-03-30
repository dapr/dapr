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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/dapr/dapr/pkg/healthz"
)

func TestConfigCorrectValues(t *testing.T) {
	i, err := NewInjector(Options{
		Config: Config{
			SidecarImage:                      "c",
			SidecarImagePullPolicy:            "d",
			Namespace:                         "e",
			AllowedServiceAccountsPrefixNames: "ns*:sa,namespace:sa*",
			ControlPlaneTrustDomain:           "trust.domain",
		},
		Healthz: healthz.New(),
	})
	require.NoError(t, err)

	injector := i.(*injector)
	assert.Equal(t, "c", injector.config.SidecarImage)
	assert.Equal(t, "d", injector.config.SidecarImagePullPolicy)
	assert.Equal(t, "e", injector.config.Namespace)
	require.NotNil(t, injector.namespaceNameMatcher, "matcher should be configured from prefix names")
	// Verify the prefix patterns match as expected.
	assert.True(t, injector.namespaceNameMatcher("ns-something", "sa"))
	assert.True(t, injector.namespaceNameMatcher("namespace", "sa-test"))
	assert.False(t, injector.namespaceNameMatcher("other", "sa"))
}

func TestConfigWithGlobPatterns(t *testing.T) {
	i, err := NewInjector(Options{
		Config: Config{
			SidecarImage:                   "img",
			Namespace:                      "ns",
			ControlPlaneTrustDomain:        "trust.domain",
			AllowedServiceAccountsPatterns: "something-*:foo*bar,prod-?:exact",
		},
		Healthz: healthz.New(),
	})
	require.NoError(t, err)

	injector := i.(*injector)
	require.NotNil(t, injector.namespaceNameMatcher)
	assert.True(t, injector.namespaceNameMatcher("something-abc", "fooXbar"))
	assert.True(t, injector.namespaceNameMatcher("something-", "foobar"))
	assert.True(t, injector.namespaceNameMatcher("prod-A", "exact"))
	assert.False(t, injector.namespaceNameMatcher("prod-AB", "exact"))
	assert.False(t, injector.namespaceNameMatcher("other", "foobar"))
}

func TestConfigWithBothPrefixAndGlobPatterns(t *testing.T) {
	i, err := NewInjector(Options{
		Config: Config{
			SidecarImage:                      "img",
			Namespace:                         "ns",
			ControlPlaneTrustDomain:           "trust.domain",
			AllowedServiceAccountsPrefixNames: "legacy-ns*:legacy-sa*",
			AllowedServiceAccountsPatterns:    "new-ns-?:new-sa-[abc]*",
		},
		Healthz: healthz.New(),
	})
	require.NoError(t, err)

	injector := i.(*injector)
	require.NotNil(t, injector.namespaceNameMatcher)
	// Legacy prefix patterns should still work.
	assert.True(t, injector.namespaceNameMatcher("legacy-ns-foo", "legacy-sa-bar"))
	// New glob patterns should work.
	assert.True(t, injector.namespaceNameMatcher("new-ns-X", "new-sa-abc"))
	assert.False(t, injector.namespaceNameMatcher("new-ns-X", "new-sa-d"))
}

func TestConfigNoExtraMatchersStillHasDefaults(t *testing.T) {
	i, err := NewInjector(Options{
		Config: Config{
			SidecarImage:            "img",
			Namespace:               "ns",
			ControlPlaneTrustDomain: "trust.domain",
		},
		Healthz: healthz.New(),
	})
	require.NoError(t, err)

	injector := i.(*injector)
	// Even with no extra config, the default allowed service accounts
	// (kube-system controllers, etc.) are always present.
	require.NotNil(t, injector.namespaceNameMatcher)
	assert.True(t, injector.namespaceNameMatcher("kube-system", "deployment-controller"))
	assert.False(t, injector.namespaceNameMatcher("unknown", "unknown"))
}

func TestNewInjectorBadAllowedGlobPatternsConfig(t *testing.T) {
	_, err := NewInjector(Options{
		Config: Config{
			SidecarImage:                   "img",
			Namespace:                      "ns",
			AllowedServiceAccountsPatterns: "ns:sa[invalid",
		},
		Healthz: healthz.New(),
	})
	require.Error(t, err)
}

func TestNewInjectorBadPatternConfig(t *testing.T) {
	_, err := NewInjector(Options{
		Config: Config{
			SidecarImage:                      "c",
			SidecarImagePullPolicy:            "d",
			Namespace:                         "e",
			AllowedServiceAccountsPrefixNames: "ns:sa-[invalid",
		},
		Healthz: healthz.New(),
	})
	require.Error(t, err)
}

func TestGetAppIDFromRequest(t *testing.T) {
	t.Run("can handle nil", func(t *testing.T) {
		appID := getAppIDFromRequest(nil)
		assert.Empty(t, appID)
	})

	t.Run("can handle empty admissionrequest object", func(t *testing.T) {
		fakeReq := &admissionv1.AdmissionRequest{}
		appID := getAppIDFromRequest(fakeReq)
		assert.Empty(t, appID)
	})

	t.Run("get appID from annotations", func(t *testing.T) {
		fakePod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"dapr.io/app-id": "fakeID",
				},
			},
		}
		rawBytes, _ := json.Marshal(fakePod)
		fakeReq := &admissionv1.AdmissionRequest{
			Object: runtime.RawExtension{
				Raw: rawBytes,
			},
		}
		appID := getAppIDFromRequest(fakeReq)
		assert.Equal(t, "fakeID", appID)
	})

	t.Run("fall back to pod name", func(t *testing.T) {
		fakePod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mypod",
			},
		}
		rawBytes, _ := json.Marshal(fakePod)
		fakeReq := &admissionv1.AdmissionRequest{
			Object: runtime.RawExtension{
				Raw: rawBytes,
			},
		}
		appID := getAppIDFromRequest(fakeReq)
		assert.Equal(t, "mypod", appID)
	})
}

func TestAllowedControllersServiceAccountUID(t *testing.T) {
	client := kubernetesfake.NewSimpleClientset()

	testCases := []struct {
		namespace string
		name      string
	}{
		{metav1.NamespaceSystem, "replicaset-controller"},
		{"tekton-pipelines", "tekton-pipelines-controller"},
		{"test", "test"},
	}

	for _, testCase := range testCases {
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testCase.name,
				Namespace: testCase.namespace,
			},
		}
		_, err := client.CoreV1().ServiceAccounts(testCase.namespace).Create(t.Context(), sa, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	t.Run("injector config has no allowed service account", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(t.Context(), Config{}, client)
		require.NoError(t, err)
		assert.Len(t, uids, 2)
	})

	t.Run("injector config has a valid allowed service account", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(t.Context(), Config{AllowedServiceAccounts: "test:test"}, client)
		require.NoError(t, err)
		assert.Len(t, uids, 3)
	})

	t.Run("injector config has a invalid allowed service account", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(t.Context(), Config{AllowedServiceAccounts: "abc:abc"}, client)
		require.NoError(t, err)
		assert.Len(t, uids, 2)
	})

	t.Run("injector config has multiple allowed service accounts", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(t.Context(), Config{AllowedServiceAccounts: "test:test,abc:abc"}, client)
		require.NoError(t, err)
		assert.Len(t, uids, 3)
	})
}
