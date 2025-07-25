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
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kauthapi "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
)

func TestValidate(t *testing.T) {
	newToken := func(t *testing.T, ns, name string) string {
		token, err := jwt.NewBuilder().Claim("kubernetes.io", map[string]any{
			"pod": map[string]any{
				"namespace": ns,
				"name":      name,
			},
		}).Build()
		require.NoError(t, err)
		tokenB, err := json.Marshal(token)
		require.NoError(t, err)
		return string(tokenB)
	}

	sentryID := spiffeid.RequireFromPath(spiffeid.RequireTrustDomainFromString("cluster.local"), "/ns/dapr-test/dapr-sentry")

	tests := map[string]struct {
		reactor func(t *testing.T) core.ReactionFunc
		req     *sentryv1pb.SignCertificateRequest
		pod     *corev1.Pod
		config  *configapi.Configuration

		sentryAudience string

		expTD  spiffeid.TrustDomain
		expAud []string
		expErr bool
	}{
		"if pod in different namespace, expect error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "not-my-ns", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "not-my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if config in different namespace, expect error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "not-my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"is missing app-id and app-id is the same as the pod name, expect no error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "my-ns", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-pod",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("public"),
		},

		"if missing app-id annotation and app-id doesn't match pod name, expect error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if target configuration doesn't exist, expect error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: nil,
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"is service account on pod does not match token, expect error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "not-my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if username namespace doesn't match request namespace, reject": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "not-my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if token returns an error, expect error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						Error:         "this is an error",
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if token auth failed, expect failed": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: false,
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},

		"if pod name is empty in kube token, expect error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", ""),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"valid authentication, no config annotation should be default trust domain": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "my-ns", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("public"),
		},
		"valid authentication, config annotation, return the trust domain from config": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "my-ns", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("example.test.dapr.io"),
		},
		"valid authentication, config annotation, config empty, return the default trust domain": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "my-ns", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("public"),
		},
		"valid authentication, if sentry return trust domain of control-plane": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:dapr-sentry",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken(t, "dapr-test", "dapr-sentry"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-sentry",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-sentry",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/control-plane": "sentry",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "dapr-sentry"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("cluster.local"),
		},
		"valid authentication, if operator return trust domain of control-plane": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:dapr-operator",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken(t, "dapr-test", "dapr-operator"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-operator",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-operator",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/control-plane": "operator",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "dapr-operator"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("cluster.local"),
		},
		"valid authentication, if injector return trust domain of control-plane": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:dapr-injector",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken(t, "dapr-test", "dapr-injector"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-injector",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-injector",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/control-plane": "injector",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "dapr-injector"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("cluster.local"),
		},
		"valid authentication, if placement return trust domain of control-plane": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:dapr-placement",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken(t, "dapr-test", "dapr-placement"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-placement",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-placement",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/control-plane": "placement",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "dapr-placement"},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("cluster.local"),
		},

		"if both app-id and control-plane annotation present, should error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-test:my-sa",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/app-id":        "my-app-id",
						"dapr.io/control-plane": "placement",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "dapr-test",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"if control plane component not in control plane namespace, error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-ns:my-sa",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/control-plane": "placement",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"should error if the control plane component is not known": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-test:my-sa",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/control-plane": "foo",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "dapr-test",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"should always use the control plane trust domain even in config is configured": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "dapr-placement",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/control-plane": "placement",
						"dapr.io/config":        "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "dapr-test",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("cluster.local"),
		},
		"injector is not able to request for whatever identity it wants (name)": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "dapr-test",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "bar",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/control-plane": "injector",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"injector is not able to request for whatever identity it wants (namespace)": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "foo",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-pod",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/control-plane": "injector",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"injector is not able to request for whatever identity it wants (namespace + name)": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "foo",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "bar",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "dapr-test",
					Annotations: map[string]string{
						"dapr.io/control-plane": "injector",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"injector is not able to request for whatever identity it wants if not in control plane namespace": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:dapr-test:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "foo",
				Token:                     newToken(t, "bar", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "bar",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "bar",
					Annotations: map[string]string{
						"dapr.io/control-plane": "injector",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"if app ID is 64 characters long don't error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        strings.Repeat("a", 64),
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": strings.Repeat("a", 64),
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("public"),
		},
		"if app ID is 65 characters long expect error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        strings.Repeat("a", 65),
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": strings.Repeat("a", 65),
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"if app ID is 0 characters long expect error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "",
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"if requester is using the legacy request ID, and namespace+service account name is over 64 characters, error": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:" + strings.Repeat("a", 65),
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "dapr-test", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-ns:" + strings.Repeat("a", 65),
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
				},
				Spec: corev1.PodSpec{ServiceAccountName: strings.Repeat("a", 65)},
			},
			expErr: true,
			expTD:  spiffeid.TrustDomain{},
		},
		"valid authentication, config with audiences should return audiences in response": {
			sentryAudience: "spiffe://cluster.local/ns/dapr-test/dapr-sentry",
			reactor: func(t *testing.T) core.ReactionFunc {
				return func(action core.Action) (bool, runtime.Object, error) {
					obj := action.(core.CreateAction).GetObject().(*kauthapi.TokenReview)
					assert.Equal(t, []string{"dapr.io/sentry", "spiffe://cluster.local/ns/dapr-test/dapr-sentry"}, obj.Spec.Audiences)
					return true, &kauthapi.TokenReview{Status: kauthapi.TokenReviewStatus{
						Authenticated: true,
						User: kauthapi.UserInfo{
							Username: "system:serviceaccount:my-ns:my-sa",
						},
					}}, nil
				}
			},
			req: &sentryv1pb.SignCertificateRequest{
				CertificateSigningRequest: []byte("csr"),
				Namespace:                 "my-ns",
				Token:                     newToken(t, "my-ns", "my-pod"),
				TrustDomain:               "example.test.dapr.io",
				Id:                        "my-app-id",
				JwtAudiences:              []string{"custom-audience-1", "custom-audience-2"},
			},
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-pod",
					Namespace: "my-ns",
					Annotations: map[string]string{
						"dapr.io/app-id": "my-app-id",
						"dapr.io/config": "my-config",
					},
				},
				Spec: corev1.PodSpec{ServiceAccountName: "my-sa"},
			},
			config: &configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-config",
					Namespace: "my-ns",
				},
				Spec: configapi.ConfigurationSpec{
					AccessControlSpec: &configapi.AccessControlSpec{
						TrustDomain: "example.test.dapr.io",
					},
				},
			},
			expErr: false,
			expTD:  spiffeid.RequireTrustDomainFromString("example.test.dapr.io"),
			expAud: []string{"custom-audience-1", "custom-audience-2"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var kobjs []runtime.Object
			if test.pod != nil {
				kobjs = append(kobjs, test.pod)
			}

			kubeCl := kubefake.NewSimpleClientset(kobjs...)
			kubeCl.Fake.PrependReactor("create", "tokenreviews", test.reactor(t))
			scheme := runtime.NewScheme()
			require.NoError(t, configapi.AddToScheme(scheme))
			require.NoError(t, corev1.AddToScheme(scheme))
			client := clientfake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(kobjs...).Build()

			if test.config != nil {
				require.NoError(t, client.Create(t.Context(), test.config))
			}

			k := &kubernetes{
				auth:           kubeCl.AuthenticationV1(),
				client:         client,
				controlPlaneNS: "dapr-test",
				controlPlaneTD: sentryID.TrustDomain(),
				sentryAudience: test.sentryAudience,
				ready:          func(_ context.Context) bool { return true },
			}

			res, err := k.Validate(t.Context(), test.req)
			assert.Equal(t, test.expErr, err != nil, "%v", err)
			assert.Equal(t, test.expTD, res.TrustDomain, "%v", res.TrustDomain)
		})
	}
}

func Test_isControlPlaneService(t *testing.T) {
	tests := map[string]struct {
		name string
		exp  bool
	}{
		"operator should be control plane service": {
			name: "operator",
			exp:  true,
		},
		"sentry should be control plane service": {
			name: "sentry",
			exp:  true,
		},
		"placement should be control plane service": {
			name: "placement",
			exp:  true,
		},
		"sidecar injector should be control plane service": {
			name: "injector",
			exp:  true,
		},
		"not a control plane service": {
			name: "my-app",
			exp:  false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.exp, isControlPlaneService(test.name))
		})
	}
}
