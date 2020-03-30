package operator

import (
	"os"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	certchain "github.com/dapr/dapr/pkg/credentials"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Config struct {
	MTLSEnabled bool
	Credentials certchain.TLSCredentials
}

func LoadConfiguration(name string, client scheme.Interface) (*Config, error) {
	namespace := os.Getenv("NAMESPACE")
	conf, err := client.ConfigurationV1alpha1().Configurations(namespace).Get(name, v1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &Config{
		MTLSEnabled: conf.Spec.MTLSSpec.Enabled,
	}, nil
}
