package keyvault

import (
	"context"
	"fmt"

	"github.com/actionscore/actions/pkg/components/secretstores"

	kv "github.com/Azure/azure-sdk-for-go/profiles/latest/keyvault/keyvault"
)

// Keyvault secret store component metadata properties
const (
	ComponentVaultName          = "vaultName"
	ComponentSPNCertificateFile = "spnCertificateFile"
	ComponentSPNCertificate     = "spnCertificate"
	ComponentSPNTenantID        = "spnTenantId"
	ComponentSPNClientID        = "spnClientId"
)

type keyvaultSecretStore struct {
	vaultName        string
	vaultClient      kv.BaseClient
	clientAuthorizer ClientAuthorizer
}

// NewKeyvaultSecretStore returns a new Kubernetes secret store
func NewKeyvaultSecretStore() secretstores.SecretStore {
	return &keyvaultSecretStore{
		vaultName:   "",
		vaultClient: kv.New(),
	}
}

// Init creates a Kubernetes client
func (k *keyvaultSecretStore) Init(metadata secretstores.Metadata) error {
	props := metadata.Properties
	k.vaultName = props[ComponentVaultName]
	certFilePath := props[ComponentSPNCertificateFile]
	certBytes := []byte(props[ComponentSPNCertificate])
	certPassword := ""

	k.clientAuthorizer = NewClientAuthorizer(
		certFilePath,
		certBytes,
		certPassword,
		props[ComponentSPNClientID],
		props[ComponentSPNTenantID])

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

	resp := secretstores.GetSecretResponse{
		Data: map[string]string{
			secretstores.DefaultSecretRefKeyName: *secretResp.Value,
		},
	}
	return resp, nil
}

// getVaultURI returns Azure Key Vault URI
func (k *keyvaultSecretStore) getVaultURI() string {
	return fmt.Sprintf("https://%s.vault.azure.net", k.vaultName)
}
