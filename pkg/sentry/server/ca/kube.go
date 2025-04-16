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
func (k *kube) get(ctx context.Context) (Bundle, generate, error) {
	// Get the trust bundle secret
	secret, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return Bundle{}, generate{}, fmt.Errorf("failed to get trust bundle secret: %w", err)
	}

	// Check if X.509 certificates need to be generated
	needsX509 := false
	trustAnchors, hasRootCert := secret.Data[filepath.Base(k.config.RootCertPath)]
	issChainPEM, hasIssuerCert := secret.Data[filepath.Base(k.config.IssuerCertPath)]
	issKeyPEM, hasIssuerKey := secret.Data[filepath.Base(k.config.IssuerKeyPath)]

	if !hasRootCert || !hasIssuerCert || !hasIssuerKey {
		needsX509 = true
	}

	// Also check if the ConfigMap is in sync
	configMap, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return Bundle{}, generate{}, err
	}

	if configMapRootCert, ok := configMap.Data[filepath.Base(k.config.RootCertPath)]; !ok || (hasRootCert && configMapRootCert != string(trustAnchors)) {
		needsX509 = true
	}

	// Create a bundle if certificates are available
	var bundle Bundle
	if !needsX509 {
		bundle, err = verifyBundle(trustAnchors, issChainPEM, issKeyPEM)
		if err != nil {
			return Bundle{}, generate{}, fmt.Errorf("failed to verify CA bundle: %w", err)
		}
	}

	// Check for JWT signing key and JWKS
	needsJWT := false

	// Process JWT signing key if available
	if jwtKeyPEM, ok := secret.Data[filepath.Base(k.config.JWTSigningKeyPath)]; ok {
		jwtKey, err := loadJWTSigningKey(jwtKeyPEM)
		if err != nil {
			return Bundle{}, generate{}, fmt.Errorf("failed to load JWT signing key: %w", err)
		}
		bundle.JWTSigningKey = jwtKey
		bundle.JWTSigningKeyPEM = jwtKeyPEM

		log.Warnf("!! Set JWT signing key from secret %s", TrustBundleK8sName)
	} else {
		needsJWT = true
	}

	// Process JWKS if available
	if jwks, ok := secret.Data[filepath.Base(k.config.JWKSPath)]; ok {
		log.Warnf("!! Verifying JWKS %s", string(jwks))
		if err := verifyJWKS(jwks, bundle.JWTSigningKey); err != nil {
			return Bundle{}, generate{}, fmt.Errorf("failed to verify JWKS: %w", err)
		}
		bundle.JWKSJson = jwks
	} else {
		// clear the JWT signing key if JWKS is not available
		bundle.JWTSigningKey = nil
		bundle.JWTSigningKeyPEM = nil

		needsJWT = true
	}

	return bundle, generate{
		x509: needsX509,
		jwt:  needsJWT,
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

	if _, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, configMap, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update trust bundle configmap: %w", err)
	}

	return nil
}
