package certs

import (
	"context"
	"fmt"
	"os"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/consts"
	"github.com/dapr/dapr/pkg/sentry/kubernetes"
)

const (
	defaultSecretNamespace = "default"
)

// StoreCredentials saves the trust bundle in a Kubernetes secret store or locally on disk, depending on the hosting platform.
func StoreCredentials(conf config.SentryConfig, rootCertPem, issuerCertPem, issuerKeyPem []byte, clientCertPem, clientCertKey []byte) error {
	if config.IsKubernetesHosted() {
		return storeKubernetes(rootCertPem, issuerCertPem, issuerKeyPem, clientCertPem, clientCertKey)
	}
	return storeSelfhosted(rootCertPem, issuerCertPem, issuerKeyPem, clientCertPem, clientCertKey, conf.RootCertPath, conf.IssuerCertPath, conf.IssuerKeyPath, conf.ClientCertPath, conf.ClientKeyPath)
}

func storeKubernetes(rootCertPem, issuerCertPem, issuerCertKey, clientCertPem, clientCertKey []byte) error {
	kubeClient, err := kubernetes.GetClient()
	if err != nil {
		return err
	}

	namespace := getNamespace()
	secret := &v1.Secret{
		Data: map[string][]byte{
			credentials.RootCertFilename:   rootCertPem,
			credentials.IssuerCertFilename: issuerCertPem,
			credentials.IssuerKeyFilename:  issuerCertKey,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      consts.TrustBundleK8sSecretName,
			Namespace: namespace,
		},
		Type: v1.SecretTypeOpaque,
	}

	// We update and not create because sentry expects a secret to already exist
	_, err = kubeClient.CoreV1().Secrets(namespace).Update(context.TODO(), secret, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed saving secret to kubernetes: %w", err)
	}

	clientSecret := &v1.Secret{
		Data: map[string][]byte{
			credentials.RootCertFilename:   rootCertPem,
			credentials.ClientCertFilename: append(clientCertPem, issuerCertPem...),
			credentials.ClientKeyFilename:  clientCertKey,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      consts.ClientBundleK8sSecretName,
			Namespace: namespace,
		},
		Type: v1.SecretTypeOpaque,
	}
	s, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), consts.TrustBundleK8sSecretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed get client secret from kubernetes: %w", err)
	}
	exist := len(s.Data) > 0
	// since this is newly added, we need to check if the secret already exists
	if exist {
		_, err = kubeClient.CoreV1().Secrets(namespace).Update(context.TODO(), clientSecret, metav1.UpdateOptions{})
	} else {
		_, err = kubeClient.CoreV1().Secrets(namespace).Create(context.TODO(), clientSecret, metav1.CreateOptions{})
	}
	if err != nil {
		return fmt.Errorf("failed saving client secret to kubernetes: %w", err)
	}
	return nil
}

func getNamespace() string {
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = defaultSecretNamespace
	}
	return namespace
}

// CredentialsExist checks root and issuer credentials exist on a hosting platform.
func CredentialsExist(conf config.SentryConfig) (bool, error) {
	if config.IsKubernetesHosted() {
		namespace := getNamespace()

		kubeClient, err := kubernetes.GetClient()
		if err != nil {
			return false, err
		}
		s, err := kubeClient.CoreV1().Secrets(namespace).Get(context.TODO(), consts.TrustBundleK8sSecretName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return len(s.Data) > 0, nil
	}
	return false, nil
}

/* #nosec. */
func storeSelfhosted(rootCertPem, issuerCertPem, issuerKeyPem, clientCertPem, clientKeyPem []byte, rootCertPath, issuerCertPath, issuerKeyPath, clientCertPath, clientKeyPath string) error {
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

	// write client cert with its intermediate ca cert
	err = os.WriteFile(clientCertPath, append(clientCertPem, issuerCertPem...), 0o644)
	if err != nil {
		return fmt.Errorf("failed saving file to %s err: %w", clientCertPath, err)
	}

	err = os.WriteFile(clientKeyPath, clientKeyPem, 0o644)
	if err != nil {
		return fmt.Errorf("failed saving file to %s err: %w", clientKeyPath, err)
	}
	return nil
}
