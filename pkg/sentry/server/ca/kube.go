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
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	"github.com/dapr/dapr/pkg/sentry/config"
	bundle "github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/kit/crypto/pem"
)

const (
	// TrustBundleK8sName is the name of the kubernetes secret that holds the
	// issuer certificate key pair and trust anchors, and configmap that holds
	// the trust anchors.
	TrustBundleK8sName = "dapr-trust-bundle" /* #nosec */

	// Rotation state keys stored in the trust-bundle Secret.
	rotationPhaseKey           = "rotation.phase"          /* #nosec */
	rotationNewCACertKey       = "rotation.new-ca.crt"     /* #nosec */
	rotationNewIssCertKey      = "rotation.new-issuer.crt" /* #nosec */
	rotationNewIssKeyKey       = "rotation.new-issuer.key" /* #nosec */
	rotationDistributedAtKey   = "rotation.distributed-at"
	rotationSigningAtKey       = "rotation.signing-at"
	rotationOldRootNotAfterKey = "rotation.old-root-not-after"
)

// kube is a store that uses Kubernetes as the secret store.
type kube struct {
	config    config.Config
	namespace string
	client    kubernetes.Interface
}

// get retrieves the existing certificate bundle from Kubernetes.
func (k *kube) get(ctx context.Context) (bundle.Bundle, error) {
	// Get the trust bundle secret
	secret, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return bundle.Bundle{}, fmt.Errorf("failed to get trust bundle secret: %w", err)
	}

	// Check if X.509 certificates need to be generated
	trustAnchors, hasRootCert := secret.Data[filepath.Base(k.config.RootCertPath)]
	issChainPEM, hasIssuerCert := secret.Data[filepath.Base(k.config.IssuerCertPath)]
	issKeyPEM, hasIssuerKey := secret.Data[filepath.Base(k.config.IssuerKeyPath)]

	generateX509 := !hasRootCert || !hasIssuerCert || !hasIssuerKey

	// Also check if the ConfigMap is in sync
	configMap, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return bundle.Bundle{}, err
	}

	if configMapRootCert, ok := configMap.Data[filepath.Base(k.config.RootCertPath)]; !ok || (hasRootCert && configMapRootCert != string(trustAnchors)) {
		generateX509 = true
	}

	// Create a bundle if certificates are available
	var bndle bundle.Bundle
	if !generateX509 {
		bndle.X509, err = verifyX509Bundle(trustAnchors, issChainPEM, issKeyPEM)
		if err != nil {
			return bundle.Bundle{}, fmt.Errorf("failed to verify CA bundle: %w", err)
		}
	}

	// Check for JWT signing key and JWKS
	jwtKeyPEM, hasJWTKey := secret.Data[filepath.Base(k.config.JWT.SigningKeyPath)]
	jwks, hasJWKS := secret.Data[filepath.Base(k.config.JWT.JWKSPath)]

	if hasJWTKey && hasJWKS {
		jwtKey, jwtErr := loadJWTSigningKey(jwtKeyPEM)
		if jwtErr != nil {
			return bundle.Bundle{}, fmt.Errorf("failed to load JWT signing key: %w", jwtErr)
		}

		if verifyErr := verifyJWKS(jwks, jwtKey, k.config.JWT.KeyID); verifyErr != nil {
			return bundle.Bundle{}, fmt.Errorf("failed to verify JWKS: %w", verifyErr)
		}

		bndle.JWT = &bundle.JWT{
			SigningKey:    jwtKey,
			SigningKeyPEM: jwtKeyPEM,
			JWKSJson:      jwks,
		}
		bndle.JWT.JWKS, err = jwk.Parse(jwks)
		if err != nil {
			return bundle.Bundle{}, fmt.Errorf("failed to parse JWKS: %w", err)
		}
	}

	// Load rotation state if it is present in the Secret.
	if phase, ok := secret.Data[rotationPhaseKey]; ok && len(phase) > 0 {
		// Malformed timestamps must invalidate the rotation state rather than
		// silently becoming the zero time: a zero SigningAt or OldRootNotAfter
		// would make the cleanup conditions appear satisfied and remove the old
		// root CA prematurely.
		distributedAt, err := unmarshalTime(secret.Data[rotationDistributedAtKey])
		if err != nil {
			return bundle.Bundle{}, fmt.Errorf("invalid rotation state in trust bundle secret: %w", err)
		}
		signingAt, err := unmarshalTime(secret.Data[rotationSigningAtKey])
		if err != nil {
			return bundle.Bundle{}, fmt.Errorf("invalid rotation state in trust bundle secret: %w", err)
		}
		oldRootNotAfter, err := unmarshalTime(secret.Data[rotationOldRootNotAfterKey])
		if err != nil {
			return bundle.Bundle{}, fmt.Errorf("invalid rotation state in trust bundle secret: %w", err)
		}

		rot := &bundle.RotationState{
			Phase:           bundle.RotationPhase(phase),
			NewTrustAnchors: secret.Data[rotationNewCACertKey],
			NewIssChainPEM:  secret.Data[rotationNewIssCertKey],
			NewIssKeyPEM:    secret.Data[rotationNewIssKeyKey],
			DistributedAt:   distributedAt,
			SigningAt:       signingAt,
			OldRootNotAfter: oldRootNotAfter,
		}
		if err = validateRotationState(rot); err != nil {
			return bundle.Bundle{}, fmt.Errorf("invalid rotation state in trust bundle secret: %w", err)
		}
		newX509, verifyErr := verifyX509Bundle(rot.NewTrustAnchors, rot.NewIssChainPEM, rot.NewIssKeyPEM)
		if verifyErr != nil {
			return bundle.Bundle{}, fmt.Errorf("failed to verify rotation bundle: %w", verifyErr)
		}
		rot.NewIssChain = newX509.IssChain
		rot.NewIssKey = newX509.IssKey
		bndle.Rotation = rot
	}

	return bndle, nil
}

// store saves the certificate bundle to Kubernetes.
//
// The ConfigMap is updated before the Secret: the ConfigMap is the
// propagation mechanism for trust anchors to workloads, while the Secret
// carries the rotation state and acts as the state machine's commit point —
// analogous to rotation-state.json being written last in standalone mode. A
// failure in between leaves no persisted rotation state, so the next check
// simply retries; the ConfigMap is best-effort restored in that case to keep
// the two objects in sync.
func (k *kube) store(ctx context.Context, bundle bundle.Bundle) error {
	// Update the ConfigMap, which contains the public root certificate for
	// other components. During rotation the combined (old+new) trust anchors
	// are written here so that pods mounting this ConfigMap as a volume pick
	// up both root CAs automatically.
	configMap, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get trust bundle configmap: %w", err)
	}

	prevConfigMapData := make(map[string]string, len(configMap.Data))
	for key, value := range configMap.Data {
		prevConfigMapData[key] = value
	}

	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}

	configMap.Data[filepath.Base(k.config.RootCertPath)] = string(bundle.X509.TrustAnchors)

	// If the OIDC server is enabled, clients could use that to access the JWKS
	// to verify JWTs. However, the OIDC server is not required and so it is
	// useful to also distribute the JWKS in the configmap.
	delete(configMap.Data, filepath.Base(k.config.JWT.JWKSPath))
	if bundle.JWT != nil {
		configMap.Data[filepath.Base(k.config.JWT.JWKSPath)] = string(bundle.JWT.JWKSJson)
	}

	configMap, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update trust bundle configmap: %w", err)
	}

	// Update the Secret with all certificate data
	secret, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		k.restoreConfigMap(ctx, configMap, prevConfigMapData)
		return fmt.Errorf("failed to get trust bundle secret: %w", err)
	}

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}

	// Add all required certificates and keys
	secret.Data[filepath.Base(k.config.RootCertPath)] = bundle.X509.TrustAnchors
	secret.Data[filepath.Base(k.config.IssuerCertPath)] = bundle.X509.IssChainPEM
	secret.Data[filepath.Base(k.config.IssuerKeyPath)] = bundle.X509.IssKeyPEM

	// Add JWT related data if available
	if bundle.JWT != nil {
		if bundle.JWT.SigningKeyPEM != nil {
			secret.Data[filepath.Base(k.config.JWT.SigningKeyPath)] = bundle.JWT.SigningKeyPEM
		}
		if bundle.JWT.JWKSJson != nil {
			secret.Data[filepath.Base(k.config.JWT.JWKSPath)] = bundle.JWT.JWKSJson
		}
	}

	// Persist rotation state so it survives a sentry restart.
	if bundle.Rotation != nil {
		secret.Data[rotationPhaseKey] = []byte(bundle.Rotation.Phase)
		secret.Data[rotationNewCACertKey] = bundle.Rotation.NewTrustAnchors
		secret.Data[rotationNewIssCertKey] = bundle.Rotation.NewIssChainPEM
		secret.Data[rotationNewIssKeyKey] = bundle.Rotation.NewIssKeyPEM
		// Zero timestamps (e.g. SigningAt during the distributing phase) are
		// stored by omitting the key: a nil value would be serialised as JSON
		// null, which the Kubernetes API may reject, and an absent key reads
		// back as the zero time anyway.
		for key, ts := range map[string]time.Time{
			rotationDistributedAtKey:   bundle.Rotation.DistributedAt,
			rotationSigningAtKey:       bundle.Rotation.SigningAt,
			rotationOldRootNotAfterKey: bundle.Rotation.OldRootNotAfter,
		} {
			if ts.IsZero() {
				delete(secret.Data, key)
			} else {
				secret.Data[key] = marshalTime(ts)
			}
		}
	} else {
		// Clean up any leftover rotation keys.
		delete(secret.Data, rotationPhaseKey)
		delete(secret.Data, rotationNewCACertKey)
		delete(secret.Data, rotationNewIssCertKey)
		delete(secret.Data, rotationNewIssKeyKey)
		delete(secret.Data, rotationDistributedAtKey)
		delete(secret.Data, rotationSigningAtKey)
		delete(secret.Data, rotationOldRootNotAfterKey)
	}

	// Update the Secret. This is the last write: with the Secret persisted,
	// the stored state is complete and consistent with the ConfigMap.
	if _, err = k.client.CoreV1().Secrets(k.namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		k.restoreConfigMap(ctx, configMap, prevConfigMapData)
		return fmt.Errorf("failed to update trust bundle secret: %w", err)
	}

	return nil
}

// restoreConfigMap best-effort restores the trust bundle ConfigMap to its
// previous data after a failed Secret update, keeping the two objects in
// sync: a lasting mismatch between them invalidates the bundle on the next
// load.
func (k *kube) restoreConfigMap(ctx context.Context, configMap *corev1.ConfigMap, prevData map[string]string) {
	configMap.Data = prevData
	if _, err := k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, configMap, metav1.UpdateOptions{}); err != nil {
		log.Warnf("Failed to restore trust bundle ConfigMap after failed Secret update: %v", err)
	}
}

// verifyPropagation checks that the trust bundle ConfigMap (the
// operator-synced copy workloads mount for their trust anchors) in every
// namespace running Dapr-enabled workloads already carries the rotation's new
// root CA. The namespaces that need propagation are derived from the pods
// running Dapr sidecars: a namespace with Dapr workloads but no ConfigMap yet
// counts as lagging (its pods fell back to static trust anchors and never
// received the new root), while stale ConfigMaps in namespaces without Dapr
// workloads are ignored so they cannot block cleanup indefinitely. This runs
// at most once per rotation check while a rotation awaits cleanup.
func (k *kube) verifyPropagation(ctx context.Context, rot *bundle.RotationState) error {
	newRoots, err := pem.DecodePEMCertificates(rot.NewTrustAnchors)
	if err != nil {
		return fmt.Errorf("failed to decode pending trust anchors: %w", err)
	}

	pods, err := k.client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: injectorConsts.SidecarInjectedLabel + "=true",
	})
	if err != nil {
		return fmt.Errorf("failed to list Dapr-enabled pods: %w", err)
	}

	// The control-plane namespace holds the source ConfigMap and is always
	// required.
	namespaces := map[string]struct{}{k.namespace: {}}
	for i := range pods.Items {
		pod := &pods.Items[i]
		// The server-side selector already filters; re-check to be safe.
		if pod.Labels[injectorConsts.SidecarInjectedLabel] != "true" {
			continue
		}
		namespaces[pod.Namespace] = struct{}{}
	}

	var lagging []string
	for namespace := range namespaces {
		configMap, cmErr := k.client.CoreV1().ConfigMaps(namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
		if cmErr != nil {
			if !apierrors.IsNotFound(cmErr) {
				return fmt.Errorf("failed to get trust bundle configmap in namespace %s: %w", namespace, cmErr)
			}
			lagging = append(lagging, namespace)
			continue
		}
		anchors, decErr := pem.DecodePEMCertificates([]byte(configMap.Data[filepath.Base(k.config.RootCertPath)]))
		if decErr != nil || !containsCerts(anchors, newRoots) {
			lagging = append(lagging, namespace)
		}
	}
	if len(lagging) > 0 {
		sort.Strings(lagging)
		return fmt.Errorf("new trust anchors have not yet propagated to the trust bundle ConfigMap in namespaces %v", lagging)
	}

	return nil
}

// containsCerts returns whether every cert in want is present in have.
func containsCerts(have, want []*x509.Certificate) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h.Equal(w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// marshalTime serialises a time.Time to RFC3339 bytes for Secret storage.
func marshalTime(t time.Time) []byte {
	if t.IsZero() {
		return nil
	}
	return []byte(t.UTC().Format(time.RFC3339))
}

// unmarshalTime deserialises RFC3339 bytes from a Secret back to time.Time.
// Empty input is valid (the zero time); malformed input is an error.
func unmarshalTime(b []byte) (time.Time, error) {
	if len(b) == 0 {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, string(b))
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse rotation timestamp %q: %w", string(b), err)
	}
	return t, nil
}
