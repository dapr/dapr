package operator

import (
	"os"
	"path/filepath"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/sentry/certchain"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Config struct {
	MTLSEnabled     bool
	CredentialsPath string
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

// CredentialsPath sets the directory where the cert chain credentials should be looked in
func (o *Config) SetCredentialsPath(path string) {
	o.CredentialsPath = path
}

// RootCertPath returns the file path for the root cert
func (o *Config) RootCertPath() string {
	return filepath.Join(o.CredentialsPath, certchain.RootCertFilename)
}

// CertPath returns the file path for the cert
func (o *Config) CertPath() string {
	return filepath.Join(o.CredentialsPath, certchain.IssuerCertFilename)
}

// KeyPath returns the file path for the cert key
func (o *Config) KeyPath() string {
	return filepath.Join(o.CredentialsPath, certchain.IssuerKeyFilename)
}
