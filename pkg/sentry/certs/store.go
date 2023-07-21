package certs

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/consts"
	"github.com/dapr/dapr/pkg/sentry/kubernetes"
	"github.com/dapr/dapr/utils"
)

const (
	defaultSecretNamespace = "default"
)

// StoreCredentials saves the trust bundle in a Kubernetes secret store or locally on disk, depending on the hosting platform.
func StoreCredentials(ctx context.Context, conf config.SentryConfig, rootCertPem, issuerCertPem, issuerKeyPem []byte) error {
	if config.IsKubernetesHosted() {
		return storeKubernetes(ctx, rootCertPem, issuerCertPem, issuerKeyPem)
	}
	return storeSelfhosted(rootCertPem, issuerCertPem, issuerKeyPem, conf.RootCertPath, conf.IssuerCertPath, conf.IssuerKeyPath)
}

func storeKubernetes(ctx context.Context, rootCertPem, issuerCertPem, issuerCertKey []byte) error {
	kubeClient, err := kubernetes.GetClient()
	if err != nil {
		return err
	}

	namespace := utils.GetNamespaceOrDefault(defaultSecretNamespace)
	secret, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), consts.TrustBundleK8sSecretName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return fmt.Errorf("failed to get secret %w", err)
	}

	secret.Data = map[string][]byte{
		credentials.RootCertFilename:   rootCertPem,
		credentials.IssuerCertFilename: issuerCertPem,
		credentials.IssuerKeyFilename:  issuerCertKey,
	}

	// We update and not create because sentry expects a secret to already exist
	_, err = kubeClient.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed saving secret to kubernetes: %w", err)
	}
	return nil
}

// CredentialsExist checks root and issuer credentials exist on a hosting platform.
func CredentialsExist(ctx context.Context, conf config.SentryConfig) (bool, error) {
	if config.IsKubernetesHosted() {
		namespace := utils.GetNamespaceOrDefault(defaultSecretNamespace)

		kubeClient, err := kubernetes.GetClient()
		if err != nil {
			return false, err
		}
		s, err := kubeClient.CoreV1().Secrets(namespace).Get(ctx, consts.TrustBundleK8sSecretName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return len(s.Data) > 0, nil
	}

	for _, path := range []string{conf.RootCertPath, conf.IssuerCertPath, conf.IssuerKeyPath} {
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}

	return true, nil
}

/* #nosec. */
func storeSelfhosted(rootCertPem, issuerCertPem, issuerKeyPem []byte, rootCertPath, issuerCertPath, issuerKeyPath string) error {
	err := os.WriteFile(rootCertPath, rootCertPem, 0o644)
	if err != nil {
		return fmt.Errorf("failed saving file to %s: %w", rootCertPath, err)
	}

	err = os.WriteFile(issuerCertPath, issuerCertPem, 0o644)
	if err != nil {
		return fmt.Errorf("failed saving file to %s: %w", issuerCertPath, err)
	}

	err = os.WriteFile(issuerKeyPath, issuerKeyPem, 0o644)
	if err != nil {
		return fmt.Errorf("failed saving file to %s: %w", issuerKeyPath, err)
	}
	return nil
}
