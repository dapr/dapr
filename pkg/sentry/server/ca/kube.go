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

func (k *kube) get(ctx context.Context) (caBundle, bool, error) {
	s, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return caBundle{}, false, err
	}

	trustAnchors, ok := s.Data[filepath.Base(k.config.RootCertPath)]
	if !ok {
		return caBundle{}, false, nil
	}

	issChainPEM, ok := s.Data[filepath.Base(k.config.IssuerCertPath)]
	if !ok {
		return caBundle{}, false, nil
	}

	issKeyPEM, ok := s.Data[filepath.Base(k.config.IssuerKeyPath)]
	if !ok {
		return caBundle{}, false, nil
	}

	// Ensure ConfigMap is up to date also.
	cm, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return caBundle{}, false, err
	}
	if cm.Data[filepath.Base(k.config.RootCertPath)] != string(trustAnchors) {
		return caBundle{}, false, nil
	}

	bundle, err := verifyBundle(trustAnchors, issChainPEM, issKeyPEM)
	if err != nil {
		return caBundle{}, false, err
	}

	return bundle, true, nil
}

func (k *kube) store(ctx context.Context, bundle caBundle) error {
	s, err := k.client.CoreV1().Secrets(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	s.Data = map[string][]byte{
		filepath.Base(k.config.RootCertPath):   bundle.trustAnchors,
		filepath.Base(k.config.IssuerCertPath): bundle.issChainPEM,
		filepath.Base(k.config.IssuerKeyPath):  bundle.issKeyPEM,
	}

	_, err = k.client.CoreV1().Secrets(k.namespace).Update(ctx, s, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	cm, err := k.client.CoreV1().ConfigMaps(k.namespace).Get(ctx, TrustBundleK8sName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	cm.Data = map[string]string{
		filepath.Base(k.config.RootCertPath): string(bundle.trustAnchors),
	}

	_, err = k.client.CoreV1().ConfigMaps(k.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	return nil
}
