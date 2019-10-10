// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package keyvault

import (
	"context"
	"fmt"

	"github.com/dapr/components-contrib/secretstores"

	kv "github.com/Azure/azure-sdk-for-go/profiles/latest/keyvault/keyvault"
)

// Keyvault secret store component metadata properties
const (
	componentSPNCertificate         = "spnCertificate"
	componentSPNCertificateFile     = "spnCertificateFile"
	componentSPNCertificatePassword = "spnCertificatePassword"
	componentSPNClientID            = "spnClientId"
	componentSPNTenantID            = "spnTenantId"
	componentVaultName              = "vaultName"
)

type keyvaultSecretStore struct {
	vaultName        string
	vaultClient      kv.BaseClient
	clientAuthorizer ClientAuthorizer
}

// NewAzureKeyvaultSecretStore returns a new Kubernetes secret store
func NewAzureKeyvaultSecretStore() secretstores.SecretStore {
	return &keyvaultSecretStore{
		vaultName:   "",
		vaultClient: kv.New(),
	}
}

// Init creates a Kubernetes client
func (k *keyvaultSecretStore) Init(metadata secretstores.Metadata) error {
	props := metadata.Properties
	k.vaultName = props[componentVaultName]
	certFilePath := props[componentSPNCertificateFile]
	certBytes := []byte(props[componentSPNCertificate])
	certPassword := props[componentSPNCertificatePassword]

	k.clientAuthorizer = NewClientAuthorizer(
		certFilePath,
		certBytes,
		certPassword,
		props[componentSPNClientID],
		props[componentSPNTenantID])

	authorizer, err := k.clientAuthorizer.Authorizer()
	if err == nil {
		k.vaultClient.Authorizer = authorizer
	}

	return err
}

// GetSecret retrieves a secret using a key and returns a map of decrypted string/string values
func (k *keyvaultSecretStore) GetSecret(req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	secretResp, err := k.vaultClient.GetSecret(context.Background(), k.getVaultURI(), req.Name, "")
	if err != nil {
		return secretstores.GetSecretResponse{Data: nil}, err
	}

	secretValue := ""
	if secretResp.Value != nil {
		secretValue = *secretResp.Value
	}

	return secretstores.GetSecretResponse{
		Data: map[string]string{
			secretstores.DefaultSecretRefKeyName: secretValue,
		},
	}, nil
}

// getVaultURI returns Azure Key Vault URI
func (k *keyvaultSecretStore) getVaultURI() string {
	return fmt.Sprintf("https://%s.vault.azure.net", k.vaultName)
}
