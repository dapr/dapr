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

package kubernetes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authapi "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	prockube "github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
)

func kubeAPI(t *testing.T, bundle ca.Bundle, namespace, serviceaccount string) *prockube.Kubernetes {
	t.Helper()

	return prockube.New(t,
		prockube.WithClusterDaprConfigurationList(t, new(configapi.ConfigurationList)),
		prockube.WithDaprConfigurationGet(t, &configapi.Configuration{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "daprsystem"},
			Spec: configapi.ConfigurationSpec{
				MTLSSpec: &configapi.MTLSSpec{ControlPlaneTrustDomain: "integration.test.dapr.io"},
			},
		}),
		prockube.WithSecretGet(t, &corev1.Secret{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "dapr-trust-bundle"},
			Data: map[string][]byte{
				"ca.crt":     bundle.TrustAnchors,
				"issuer.crt": bundle.IssChainPEM,
				"issuer.key": bundle.IssKeyPEM,
			},
		}),
		prockube.WithConfigMapGet(t, &corev1.ConfigMap{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "dapr-trust-bundle"},
			Data:       map[string]string{"ca.crt": string(bundle.TrustAnchors)},
		}),
		prockube.WithClusterPodList(t, &corev1.PodList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
			Items: []corev1.Pod{
				{
					TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace, Name: "mypod",
						Annotations: map[string]string{"dapr.io/app-id": "myappid"},
					},
					Spec: corev1.PodSpec{ServiceAccountName: serviceaccount},
				},
			},
		}),
		prockube.WithPath("/apis/authentication.k8s.io/v1/tokenreviews", func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var request *authapi.TokenReview
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			require.Len(t, request.Spec.Audiences, 2)
			assert.Equal(t, "dapr.io/sentry", request.Spec.Audiences[0])
			assert.Equal(t, "spiffe://integration.test.dapr.io/ns/sentrynamespace/dapr-sentry", request.Spec.Audiences[1])

			resp, err := json.Marshal(&authapi.TokenReview{
				Status: authapi.TokenReviewStatus{
					Authenticated: true,
					User:          authapi.UserInfo{Username: fmt.Sprintf("system:serviceaccount:%s:%s", namespace, serviceaccount)},
				},
			})
			require.NoError(t, err)
			w.Header().Add("Content-Type", "application/json")
			w.Write(resp)
		}),
	)
}
