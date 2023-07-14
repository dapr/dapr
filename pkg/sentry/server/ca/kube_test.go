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

package ca

import (
	"context"
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/dapr/dapr/pkg/sentry/config"
)

func TestKube_get(t *testing.T) {
	rootPEM, rootCrt, _, rootPK := genCrt(t, "root", nil, nil)
	//nolint:dogsled
	rootPEM2, _, _, _ := genCrt(t, "root2", nil, nil)
	intPEM, intCrt, intPKPEM, intPK := genCrt(t, "int", rootCrt, rootPK)

	tests := map[string]struct {
		sec       *corev1.Secret
		cm        *corev1.ConfigMap
		expBundle Bundle
		expOK     bool
		expErr    bool
	}{
		"if secret doesn't exist, expect error": {
			sec: nil,
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: Bundle{},
			expOK:     false,
			expErr:    true,
		},
		"if configmap doesn't exist, expect error": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":  rootPEM,
					"tls.key": intPKPEM,
					"tls.crt": intPEM,
				},
			},
			cm:        nil,
			expBundle: Bundle{},
			expOK:     false,
			expErr:    true,
		},
		"if secret doesn't include ca.crt, expect not ok": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"tls.key": intPKPEM,
					"tls.crt": intPEM,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: Bundle{},
			expOK:     false,
			expErr:    false,
		},
		"if secret doesn't include tls.crt, expect not ok": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":  rootPEM,
					"tls.key": intPKPEM,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: Bundle{},
			expOK:     false,
			expErr:    false,
		},
		"if secret doesn't include tls.key, expect not ok": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":  rootPEM,
					"tls.crt": intPEM,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: Bundle{},
			expOK:     false,
			expErr:    false,
		},
		"if configmap doesn't include ca.crt, expect not ok": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":  rootPEM,
					"tls.crt": intPEM,
					"tls.key": intPKPEM,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{},
			},
			expBundle: Bundle{},
			expOK:     false,
			expErr:    false,
		},
		"if trust anchors do not match, expect not ok": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":  rootPEM,
					"tls.crt": intPEM,
					"tls.key": intPKPEM,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM) + "\n" + string(rootPEM2)},
			},
			expBundle: Bundle{},
			expOK:     false,
			expErr:    false,
		},
		"if bundle fails to verify, expect error": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":  rootPEM2,
					"tls.crt": intPEM,
					"tls.key": intPKPEM,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM2)},
			},
			expBundle: Bundle{},
			expOK:     false,
			expErr:    true,
		},
		"if bundle is valid, expect ok and return bundle": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":  rootPEM,
					"tls.crt": intPEM,
					"tls.key": intPKPEM,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: Bundle{
				TrustAnchors: rootPEM,
				IssChainPEM:  intPEM,
				IssKeyPEM:    intPKPEM,
				IssChain:     []*x509.Certificate{intCrt},
				IssKey:       intPK,
			},
			expOK:  true,
			expErr: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var intObj []runtime.Object
			if test.sec != nil {
				intObj = append(intObj, test.sec)
			}
			if test.cm != nil {
				intObj = append(intObj, test.cm)
			}

			fakeclient := fake.NewSimpleClientset(intObj...)

			k := &kube{
				client: fakeclient,
				config: config.Config{
					RootCertPath:   "ca.crt",
					IssuerCertPath: "tls.crt",
					IssuerKeyPath:  "tls.key",
				},
				namespace: "dapr-system-test",
			}

			bundle, ok, err := k.get(context.Background())
			assert.Equal(t, test.expErr, err != nil, "%v", err)
			assert.Equal(t, test.expOK, ok, "ok")
			assert.Equal(t, test.expBundle, bundle)
		})
	}
}
