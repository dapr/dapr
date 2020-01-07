// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package secretstores

import (
	"github.com/dapr/components-contrib/secretstores/azure/keyvault"
	"github.com/dapr/components-contrib/secretstores/hashicorp/vault"
	"github.com/dapr/components-contrib/secretstores/kubernetes"
)

// Load secret stores
func Load() {
	RegisterSecretStore("kubernetes", kubernetes.NewKubernetesSecretStore)
	RegisterSecretStore("azure.keyvault", keyvault.NewAzureKeyvaultSecretStore)
	RegisterSecretStore("hashicorp.vault", vault.NewHashiCorpVaultSecretStore)
}
