package secretstores

import (
	"github.com/actionscore/actions/pkg/components/secretstores"
	"github.com/actionscore/actions/pkg/secretstores/keyvault"
	"github.com/actionscore/actions/pkg/secretstores/kubernetes"
)

// Load secret stores
func Load() {
	secretstores.RegisterSecretStore("kubernetes", kubernetes.NewKubernetesSecretStore())
	secretstores.RegisterSecretStore("azure.keyvault", keyvault.NewKeyvaultSecretStore())
}
