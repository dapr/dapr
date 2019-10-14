// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package secretstores

import (
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/secretstores/keyvault"
	"github.com/dapr/components-contrib/secretstores/kubernetes"
)

// Load secret stores
func Load() {
	RegisterSecretStore("kubernetes", func() secretstores.SecretStore {
		return kubernetes.NewKubernetesSecretStore()
	})
	RegisterSecretStore("azure.keyvault", func() secretstores.SecretStore {
		return keyvault.NewAzureKeyvaultSecretStore()
	})
}
