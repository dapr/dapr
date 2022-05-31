package operator

import (
	"context"
	"os"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/credentials"
)

// Config returns an operator config options.
type Config struct {
	MTLSEnabled bool
	Credentials credentials.TLSCredentials
}

// LoadConfiguration loads the Kubernetes configuration and returns an Operator Config.
func LoadConfiguration(name string, client client.Client) (*Config, error) {
	var conf v1alpha1.Configuration
	key := types.NamespacedName{
		Namespace: os.Getenv("NAMESPACE"),
		Name:      name,
	}
	if err := client.Get(context.Background(), key, &conf); err != nil {
		return nil, err
	}
	return &Config{
		MTLSEnabled: conf.Spec.MTLSSpec.Enabled,
	}, nil
}
