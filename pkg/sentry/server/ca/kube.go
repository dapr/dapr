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
	"fmt"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

const (
	// TrustBundleK8sName is the name of the kubernetes secret that holds the
	// issuer certificate key pair and trust anchors, and configmap that holds
	// the trust anchors.
	TrustBundleK8sName = "dapr-trust-bundle" /* #nosec */
)

// kube is a store that uses Kubernetes as the secret store.
type kube struct {
	config    config.Config
	namespace string
	client    kubernetes.Interface
}

// get retrieves the existing certificate bundle from Kubernetes.
func (k *kube) get(ctx context.Context) (Bundle, CredentialGenOptions, error) {
	// Get the trust bundle secret
	secret, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to get trust bundle secret: %w", err)
	}

	// Check if X.509 certificates need to be generated
	requireX509 := false
	trustAnchors, hasRootCert := secret.Data[filepath.Base(k.config.RootCertPath)]
	issChainPEM, hasIssuerCert := secret.Data[filepath.Base(k.config.IssuerCertPath)]
	issKeyPEM, hasIssuerKey := secret.Data[filepath.Base(k.config.IssuerKeyPath)]

	if !hasRootCert || !hasIssuerCert || !hasIssuerKey {
		requireX509 = true
	}

	// Also check if the ConfigMap is in sync
	configMap, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return Bundle{}, CredentialGenOptions{}, err
	}

	if configMapRootCert, ok := configMap.Data[filepath.Base(k.config.RootCertPath)]; !ok || (hasRootCert && configMapRootCert != string(trustAnchors)) {
		requireX509 = true
	}

	// Create a bundle if certificates are available
	var bundle Bundle
	if !requireX509 {
		bundle, err = verifyBundle(trustAnchors, issChainPEM, issKeyPEM)
		if err != nil {
			return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to verify CA bundle: %w", err)
		}
	}

	// Check for JWT signing key and JWKS
	requireJWT := false

	// Process JWT signing key if available
	if jwtKeyPEM, ok := secret.Data[filepath.Base(k.config.JWTSigningKeyPath)]; ok {
		jwtKey, err := loadJWTSigningKey(jwtKeyPEM)
		if err != nil {
			return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to load JWT signing key: %w", err)
		}
		bundle.JWTSigningKey = jwtKey
		bundle.JWTSigningKeyPEM = jwtKeyPEM
	} else {
		requireJWT = true
	}

	// Process JWKS if available
	if jwks, ok := secret.Data[filepath.Base(k.config.JWKSPath)]; ok {
		if err := verifyJWKS(jwks, bundle.JWTSigningKey); err != nil {
			return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to verify JWKS: %w", err)
		}
		bundle.JWKSJson = jwks
		bundle.JWKS, err = jwk.Parse(jwks)
		if err != nil {
			return Bundle{}, CredentialGenOptions{}, fmt.Errorf("failed to parse JWKS: %w", err)
		}
	} else {
		// clear the JWT signing key if JWKS is not available
		bundle.JWTSigningKey = nil
		bundle.JWTSigningKeyPEM = nil

		requireJWT = true
	}

	return bundle, CredentialGenOptions{
		RequireX509: requireX509,
		RequireJWT:  requireJWT,
	}, nil
}

// store saves the certificate bundle to Kubernetes.
func (k *kube) store(ctx context.Context, bundle Bundle) error {
	// Update the Secret with all certificate data
	secret, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get trust bundle secret: %w", err)
	}

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}

	// Add all required certificates and keys
	secret.Data[filepath.Base(k.config.RootCertPath)] = bundle.TrustAnchors
	secret.Data[filepath.Base(k.config.IssuerCertPath)] = bundle.IssChainPEM
	secret.Data[filepath.Base(k.config.IssuerKeyPath)] = bundle.IssKeyPEM

	// Add JWT related data if available
	if bundle.JWTSigningKeyPEM != nil {
		secret.Data[filepath.Base(k.config.JWTSigningKeyPath)] = bundle.JWTSigningKeyPEM
	}
	if bundle.JWKSJson != nil {
		secret.Data[filepath.Base(k.config.JWKSPath)] = bundle.JWKSJson
	}

	// Update the Secret
	if _, err = k.client.CoreV1().Secrets(k.namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update trust bundle secret: %w", err)
	}

	// Also update ConfigMap which contains public root certificate for other components
	configMap, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get trust bundle configmap: %w", err)
	}

	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	configMap.Data[filepath.Base(k.config.RootCertPath)] = string(bundle.TrustAnchors)

	// If the OIDC server is enabled, clients could use that to access the JWKS
	// to verify JWTs. However, the OIDC server is not required and so it is
	// useful to also distribute the JWKS in the configmap.
	configMap.Data[filepath.Base(k.config.JWKSPath)] = string(bundle.JWKSJson)

	if _, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, configMap, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update trust bundle configmap: %w", err)
	}

	return nil
}
