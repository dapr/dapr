package secretstores

import (
	"github.com/actionscore/components-contrib/secretstores/keyvault"
	"github.com/actionscore/components-contrib/secretstores/kubernetes"
)

// Load secret stores
func Load() {
	RegisterSecretStore("kubernetes", kubernetes.NewKubernetesSecretStore())
	RegisterSecretStore("azure.keyvault", keyvault.NewAzureKeyvaultSecretStore())
}
