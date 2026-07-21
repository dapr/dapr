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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/dapr/dapr/pkg/sentry/config"
	ca_bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
)

func TestKube_get(t *testing.T) {
	rootPEM, rootCrt, _, rootPK := genCrt(t, "root", nil, nil)
	//nolint:dogsled
	rootPEM2, _, _, _ := genCrt(t, "root2", nil, nil)
	intPEM, intCrt, intPKPEM, intPK := genCrt(t, "int", rootCrt, rootPK)

	signingKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	jwtKeyDer, err := x509.MarshalPKCS8PrivateKey(signingKey)
	require.NoError(t, err)

	signingKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: jwtKeyDer})

	jwksBytes := createJWKS(t, signingKey, "kid")
	jwks, err := jwk.Parse(jwksBytes)
	require.NoError(t, err)

	tests := map[string]struct {
		sec       *corev1.Secret
		cm        *corev1.ConfigMap
		expBundle ca_bundle.Bundle
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
			expBundle: ca_bundle.Bundle{},
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
			expBundle: ca_bundle.Bundle{},
			expErr:    true,
		},
		"if secret doesn't include ca.crt, expect to generate x509": {
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
			expBundle: ca_bundle.Bundle{},
			expErr:    false,
		},
		"if secret doesn't include tls.crt, expect to generate x509": {
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
			expBundle: ca_bundle.Bundle{},
			expErr:    false,
		},
		"if secret doesn't include tls.key, expect to generate x509": {
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
			expBundle: ca_bundle.Bundle{},
			expErr:    false,
		},
		"if configmap doesn't include ca.crt, expect to generate x509": {
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
			expBundle: ca_bundle.Bundle{},
			expErr:    false,
		},
		"if trust anchors do not match, expect not to generate x509": {
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
			expBundle: ca_bundle.Bundle{},
			expErr:    false,
		},
		"if bundle fails to verify x509, expect error": {
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
			expBundle: ca_bundle.Bundle{},
			expErr:    true,
		},
		"if x509 only bundle is valid, expect to not generate x509 and return bundle": {
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
			expBundle: ca_bundle.Bundle{
				X509: &ca_bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
			},
			expErr: false,
		},
		"if secret doesn't include jwt.key, expect to generate jwt": {
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
			expBundle: ca_bundle.Bundle{
				X509: &ca_bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
			},
			expErr: false,
		},
		"if secret doesn't include jwks.json, expect to generate jwt": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":  rootPEM,
					"tls.crt": intPEM,
					"tls.key": intPKPEM,
					"jwt.key": signingKeyPEM,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: ca_bundle.Bundle{
				X509: &ca_bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
			},
			expErr: false,
		},
		"if secret doesn't include jwt.key or jwks.json, expect to generate jwt": {
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
			expBundle: ca_bundle.Bundle{
				X509: &ca_bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
			},
			expErr: false,
		},
		"if jwt.key is invalid, expect error": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":    rootPEM,
					"tls.crt":   intPEM,
					"tls.key":   intPKPEM,
					"jwt.key":   intPKPEM,
					"jwks.json": jwksBytes,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: ca_bundle.Bundle{},
			expErr:    true,
		},
		"if jwks.json is invalid, expect error": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":    rootPEM,
					"tls.crt":   intPEM,
					"tls.key":   intPKPEM,
					"jwt.key":   signingKeyPEM,
					"jwks.json": intPKPEM,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: ca_bundle.Bundle{},
			expErr:    true,
		},
		"valid bundle with both x509 and jwt components": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"ca.crt":    rootPEM,
					"tls.crt":   intPEM,
					"tls.key":   intPKPEM,
					"jwt.key":   signingKeyPEM,
					"jwks.json": jwksBytes,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: ca_bundle.Bundle{
				X509: &ca_bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
				JWT: &ca_bundle.JWT{
					SigningKey:    signingKey,
					SigningKeyPEM: signingKeyPEM,
					JWKS:          jwks,
					JWKSJson:      jwksBytes,
				},
			},
			expErr: false,
		},
		"missing both x509 and jwt components": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: ca_bundle.Bundle{},
			expErr:    false,
		},
		"only jwt components present": {
			sec: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string][]byte{
					"jwt.key":   signingKeyPEM,
					"jwks.json": jwksBytes,
				},
			},
			cm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "dapr-trust-bundle",
					Namespace: "dapr-system-test",
				},
				Data: map[string]string{"ca.crt": string(rootPEM)},
			},
			expBundle: ca_bundle.Bundle{
				JWT: &ca_bundle.JWT{
					SigningKey:    signingKey,
					SigningKeyPEM: signingKeyPEM,
					JWKS:          jwks,
					JWKSJson:      jwksBytes,
				},
			},
			expErr: false,
		},
		"only x509 components present with jwt keys requested": {
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
			expBundle: ca_bundle.Bundle{
				X509: &ca_bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
			},
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
					JWT: config.ConfigJWT{
						SigningKeyPath: "jwt.key",
						JWKSPath:       "jwks.json",
						TTL:            config.DefaultJWTTTL,
					},
				},
				namespace: "dapr-system-test",
			}

			bundle, err := k.get(t.Context())
			assert.Equal(t, test.expErr, err != nil, "expected error: %v, but got %v", test.expErr, err)
			bundlesEqual(t, test.expBundle, bundle)
		})
	}
}

func bundlesEqual(t *testing.T, expected, actual ca_bundle.Bundle) {
	t.Helper()

	require.Equal(t, expected.X509 == nil, actual.X509 == nil)
	if expected.X509 != nil {
		assert.Equal(t, expected.X509.TrustAnchors, actual.X509.TrustAnchors)
		assert.Equal(t, expected.X509.IssChainPEM, actual.X509.IssChainPEM)
		assert.Equal(t, expected.X509.IssKeyPEM, actual.X509.IssKeyPEM)
		assert.Len(t, actual.X509.IssChain, len(expected.X509.IssChain))
		for i := range expected.X509.IssChain {
			assert.Equal(t, expected.X509.IssChain[i].Subject, actual.X509.IssChain[i].Subject)
		}
	}
	assert.Equal(t, expected.JWT, actual.JWT)
}

func TestKube_getRotationState(t *testing.T) {
	t.Setenv("NAMESPACE", "test-ns")

	base := makeTestX509Bundle(t, time.Now().Add(365*24*time.Hour))
	pending := makeTestX509Bundle(t, time.Now().Add(365*24*time.Hour))

	newKube := func(t *testing.T, extraSecretData map[string][]byte) *kube {
		t.Helper()

		secretData := map[string][]byte{
			"ca.crt":                  base.X509.TrustAnchors,
			"tls.crt":                 base.X509.IssChainPEM,
			"tls.key":                 base.X509.IssKeyPEM,
			"rotation.phase":          []byte(ca_bundle.RotationPhaseDistributing),
			"rotation.new-ca.crt":     pending.X509.TrustAnchors,
			"rotation.new-issuer.crt": pending.X509.IssChainPEM,
			"rotation.new-issuer.key":     pending.X509.IssKeyPEM,
			"rotation.distributed-at":     []byte("2026-01-01T00:00:00Z"),
			"rotation.old-root-not-after": []byte("2027-01-01T00:00:00Z"),
		}
		for k, v := range extraSecretData {
			secretData[k] = v
		}

		fakeclient := fake.NewSimpleClientset(
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "dapr-trust-bundle", Namespace: "dapr-system-test"},
				Data:       secretData,
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "dapr-trust-bundle", Namespace: "dapr-system-test"},
				Data:       map[string]string{"ca.crt": string(base.X509.TrustAnchors)},
			},
		)

		return &kube{
			client: fakeclient,
			config: config.Config{
				RootCertPath:   "ca.crt",
				IssuerCertPath: "tls.crt",
				IssuerKeyPath:  "tls.key",
				JWT: config.ConfigJWT{
					SigningKeyPath: "jwt.key",
					JWKSPath:       "jwks.json",
					TTL:            config.DefaultJWTTTL,
				},
			},
			namespace: "dapr-system-test",
		}
	}

	t.Run("valid rotation state is loaded", func(t *testing.T) {
		k := newKube(t, nil)
		bndl, err := k.get(t.Context())
		require.NoError(t, err)
		require.NotNil(t, bndl.Rotation)
		assert.Equal(t, ca_bundle.RotationPhaseDistributing, bndl.Rotation.Phase)
		assert.Equal(t, pending.X509.TrustAnchors, bndl.Rotation.NewTrustAnchors)
		assert.Equal(t, "2026-01-01T00:00:00Z", bndl.Rotation.DistributedAt.Format(time.RFC3339))
		assert.NotNil(t, bndl.Rotation.NewIssChain)
		assert.NotNil(t, bndl.Rotation.NewIssKey)
	})

	t.Run("malformed distributed-at returns error", func(t *testing.T) {
		k := newKube(t, map[string][]byte{"rotation.distributed-at": []byte("not-a-timestamp")})
		_, err := k.get(t.Context())
		require.ErrorContains(t, err, "invalid rotation state")
	})

	t.Run("malformed signing-at returns error", func(t *testing.T) {
		k := newKube(t, map[string][]byte{"rotation.signing-at": []byte("not-a-timestamp")})
		_, err := k.get(t.Context())
		require.ErrorContains(t, err, "invalid rotation state")
	})

	t.Run("malformed old-root-not-after returns error", func(t *testing.T) {
		k := newKube(t, map[string][]byte{"rotation.old-root-not-after": []byte("not-a-timestamp")})
		_, err := k.get(t.Context())
		require.ErrorContains(t, err, "invalid rotation state")
	})
}

func TestKube_getRotationStateValidation(t *testing.T) {
	t.Setenv("NAMESPACE", "test-ns")
	// Reuse the fixture from TestKube_getRotationState via table of overrides.
	base := makeTestX509Bundle(t, time.Now().Add(365*24*time.Hour))
	pending := makeTestX509Bundle(t, time.Now().Add(365*24*time.Hour))

	newKube := func(t *testing.T, overrides map[string][]byte) *kube {
		t.Helper()
		secretData := map[string][]byte{
			"ca.crt":                      base.X509.TrustAnchors,
			"tls.crt":                     base.X509.IssChainPEM,
			"tls.key":                     base.X509.IssKeyPEM,
			"rotation.phase":              []byte(ca_bundle.RotationPhaseDistributing),
			"rotation.new-ca.crt":         pending.X509.TrustAnchors,
			"rotation.new-issuer.crt":     pending.X509.IssChainPEM,
			"rotation.new-issuer.key":     pending.X509.IssKeyPEM,
			"rotation.distributed-at":     []byte("2026-01-01T00:00:00Z"),
			"rotation.old-root-not-after": []byte("2027-01-01T00:00:00Z"),
		}
		for k, v := range overrides {
			if v == nil {
				delete(secretData, k)
			} else {
				secretData[k] = v
			}
		}
		fakeclient := fake.NewSimpleClientset(
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "dapr-trust-bundle", Namespace: "dapr-system-test"},
				Data:       secretData,
			},
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "dapr-trust-bundle", Namespace: "dapr-system-test"},
				Data:       map[string]string{"ca.crt": string(base.X509.TrustAnchors)},
			},
		)
		return &kube{
			client: fakeclient,
			config: config.Config{
				RootCertPath:   "ca.crt",
				IssuerCertPath: "tls.crt",
				IssuerKeyPath:  "tls.key",
			},
			namespace: "dapr-system-test",
		}
	}

	tests := map[string]struct {
		overrides map[string][]byte
		expErr    string
	}{
		"unknown phase": {
			overrides: map[string][]byte{"rotation.phase": []byte("bogus")},
			expErr:    "unknown rotation phase",
		},
		"missing distributed-at": {
			overrides: map[string][]byte{"rotation.distributed-at": nil},
			expErr:    "missing the distributed timestamp",
		},
		"missing old-root-not-after": {
			overrides: map[string][]byte{"rotation.old-root-not-after": nil},
			expErr:    "missing the old root CA expiry",
		},
		"signing phase without signing-at": {
			overrides: map[string][]byte{"rotation.phase": []byte(ca_bundle.RotationPhaseSigning)},
			expErr:    "missing the signing timestamp",
		},
		"missing pending credentials": {
			overrides: map[string][]byte{"rotation.new-issuer.key": nil},
			expErr:    "missing pending rotation credentials",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			k := newKube(t, test.overrides)
			_, err := k.get(t.Context())
			require.ErrorContains(t, err, test.expErr)
		})
	}
}
