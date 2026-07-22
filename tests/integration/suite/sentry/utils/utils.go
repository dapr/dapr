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

package utils

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	authapi "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	prockube "github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
)

type KubeAPIOptions struct {
	Bundle         bundle.Bundle
	Namespace      string
	ServiceAccount string
	AppID          string

	// WorkloadCertTTL and AllowedClockSkew are optionally set on the served
	// daprsystem Configuration's mTLS spec.
	WorkloadCertTTL  string
	AllowedClockSkew string

	// ExtraTrustBundleNamespaces creates additional dapr-trust-bundle
	// ConfigMaps (operator-synced copies) in the given namespaces, seeded with
	// the bundle's trust anchors and served in the cluster-wide ConfigMap
	// list. Only honored by KubeAPIRW.
	ExtraTrustBundleNamespaces []string
}

// TrustBundleRW exposes the mutable dapr-trust-bundle Secret and ConfigMap
// served by the mock Kubernetes API, for asserting on writes made by sentry
// (e.g. during root CA rotation) and for seeding state before sentry starts.
type TrustBundleRW struct {
	Secret    *prockube.ResourceRW[corev1.Secret]
	ConfigMap *prockube.ResourceRW[corev1.ConfigMap]

	// ExtraConfigMaps are additional dapr-trust-bundle ConfigMaps in other
	// namespaces (operator-synced copies), keyed by namespace. They are
	// served individually and in the cluster-wide ConfigMap list, which
	// sentry uses to verify trust anchor propagation before rotation
	// cleanup.
	ExtraConfigMaps map[string]*prockube.ResourceRW[corev1.ConfigMap]
}

func KubeAPI(t *testing.T, opts KubeAPIOptions) *prockube.Kubernetes {
	t.Helper()

	return prockube.New(t, append(kubeAPIOptions(t, opts),
		prockube.WithSecretGet(t, trustBundleSecret(opts.Bundle)),
		prockube.WithConfigMapGet(t, trustBundleConfigMap(opts.Bundle)),
	)...)
}

// KubeAPIRW is like KubeAPI, but serves the dapr-trust-bundle Secret and
// ConfigMap read-write so sentry can persist trust bundle updates and read
// them back. It also serves the cluster-wide ConfigMap list sentry uses to
// verify trust anchor propagation before rotation cleanup.
func KubeAPIRW(t *testing.T, opts KubeAPIOptions) (*prockube.Kubernetes, TrustBundleRW) {
	t.Helper()

	rw := TrustBundleRW{
		Secret:          prockube.NewSecretRW(t, trustBundleSecret(opts.Bundle)),
		ConfigMap:       prockube.NewConfigMapRW(t, trustBundleConfigMap(opts.Bundle)),
		ExtraConfigMaps: make(map[string]*prockube.ResourceRW[corev1.ConfigMap]),
	}

	kubeOpts := append(kubeAPIOptions(t, opts),
		rw.Secret.Option(),
		rw.ConfigMap.Option(),
	)

	for _, namespace := range opts.ExtraTrustBundleNamespaces {
		configMap := trustBundleConfigMap(opts.Bundle)
		configMap.Namespace = namespace
		extra := prockube.NewConfigMapRW(t, configMap)
		rw.ExtraConfigMaps[namespace] = extra
		kubeOpts = append(kubeOpts, extra.Option())
	}

	// Cluster-wide ConfigMap list aggregating the current state of every
	// trust bundle ConfigMap.
	kubeOpts = append(kubeOpts, prockube.WithPath("/api/v1/configmaps", func(w http.ResponseWriter, r *http.Request) {
		list := corev1.ConfigMapList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMapList"},
			Items:    []corev1.ConfigMap{*rw.ConfigMap.Current(t)},
		}
		for _, extra := range rw.ExtraConfigMaps {
			list.Items = append(list.Items, *extra.Current(t))
		}
		resp, err := json.Marshal(list)
		assert.NoError(t, err)
		w.Header().Add("Content-Type", "application/json")
		w.Write(resp)
	}))

	kubeAPI := prockube.New(t, kubeOpts...)

	return kubeAPI, rw
}

func trustBundleSecret(bndle bundle.Bundle) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "dapr-trust-bundle"},
		Data: map[string][]byte{
			"ca.crt":     bndle.X509.TrustAnchors,
			"issuer.crt": bndle.X509.IssChainPEM,
			"issuer.key": bndle.X509.IssKeyPEM,
		},
	}
}

func trustBundleConfigMap(bndle bundle.Bundle) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "dapr-trust-bundle"},
		Data:       map[string]string{"ca.crt": string(bndle.X509.TrustAnchors)},
	}
}

func kubeAPIOptions(t *testing.T, opts KubeAPIOptions) []prockube.Option {
	t.Helper()

	var workloadCertTTL, allowedClockSkew *string
	if opts.WorkloadCertTTL != "" {
		workloadCertTTL = &opts.WorkloadCertTTL
	}
	if opts.AllowedClockSkew != "" {
		allowedClockSkew = &opts.AllowedClockSkew
	}

	return []prockube.Option{
		prockube.WithClusterDaprConfigurationList(t, new(configapi.ConfigurationList)),
		prockube.WithDaprConfigurationGet(t, &configapi.Configuration{
			TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "Configuration"},
			ObjectMeta: metav1.ObjectMeta{Namespace: "sentrynamespace", Name: "daprsystem"},
			Spec: configapi.ConfigurationSpec{
				MTLSSpec: &configapi.MTLSSpec{
					ControlPlaneTrustDomain: "integration.test.dapr.io",
					WorkloadCertTTL:         workloadCertTTL,
					AllowedClockSkew:        allowedClockSkew,
				},
			},
		}),
		prockube.WithClusterPodList(t, &corev1.PodList{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
			Items: []corev1.Pod{
				{
					TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: opts.Namespace, Name: "mypod",
						Annotations: map[string]string{"dapr.io/app-id": opts.AppID},
					},
					Spec: corev1.PodSpec{ServiceAccountName: opts.ServiceAccount},
				},
			},
		}),
		prockube.WithPath("/apis/authentication.k8s.io/v1/tokenreviews", func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var request *authapi.TokenReview
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			if !assert.Len(t, request.Spec.Audiences, 2) {
				return
			}
			assert.Equal(t, "dapr.io/sentry", request.Spec.Audiences[0])
			assert.Equal(t, "spiffe://integration.test.dapr.io/ns/sentrynamespace/dapr-sentry", request.Spec.Audiences[1])

			resp, err := json.Marshal(&authapi.TokenReview{
				Status: authapi.TokenReviewStatus{
					Authenticated: true,
					User:          authapi.UserInfo{Username: fmt.Sprintf("system:serviceaccount:%s:%s", opts.Namespace, opts.ServiceAccount)},
				},
			})
			assert.NoError(t, err)
			w.Header().Add("Content-Type", "application/json")
			w.Write(resp)
		}),
	}
}
