package injector

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetInectorConfig(t *testing.T) {
	t.Run("with kube cluster domain env", func(t *testing.T) {
		os.Setenv("TLS_CERT_FILE", "test-cert-file")
		os.Setenv("TLS_KEY_FILE", "test-key-file")
		os.Setenv("SIDECAR_IMAGE", "daprd-test-image")
		os.Setenv("SIDECAR_IMAGE_PULL_POLICY", "Always")
		os.Setenv("NAMESPACE", "test-namespace")
		os.Setenv("KUBE_CLUSTER_DOMAIN", "cluster.local")
		defer clearenv()

		cfg, err := GetConfig()
		assert.Nil(t, err)
		assert.Equal(t, "test-cert-file", cfg.TLSCertFile)
		assert.Equal(t, "test-key-file", cfg.TLSKeyFile)
		assert.Equal(t, "daprd-test-image", cfg.SidecarImage)
		assert.Equal(t, "Always", cfg.SidecarImagePullPolicy)
		assert.Equal(t, "test-namespace", cfg.Namespace)
		assert.Equal(t, "cluster.local", cfg.KubeClusterDomain)
	})

	t.Run("not set kube cluster domain env", func(t *testing.T) {
		os.Setenv("TLS_CERT_FILE", "test-cert-file")
		os.Setenv("TLS_KEY_FILE", "test-key-file")
		os.Setenv("SIDECAR_IMAGE", "daprd-test-image")
		os.Setenv("SIDECAR_IMAGE_PULL_POLICY", "IfNotPresent")
		os.Setenv("NAMESPACE", "test-namespace")
		os.Setenv("KUBE_CLUSTER_DOMAIN", "")
		defer clearenv()

		cfg, err := GetConfig()
		assert.Nil(t, err)
		assert.Equal(t, "test-cert-file", cfg.TLSCertFile)
		assert.Equal(t, "test-key-file", cfg.TLSKeyFile)
		assert.Equal(t, "daprd-test-image", cfg.SidecarImage)
		assert.Equal(t, "IfNotPresent", cfg.SidecarImagePullPolicy)
		assert.Equal(t, "test-namespace", cfg.Namespace)
		assert.NotEqual(t, "", cfg.KubeClusterDomain)
	})
}

func clearenv() {
	os.Unsetenv("TLS_CERT_FILE")
	os.Unsetenv("TLS_KEY_FILE")
	os.Unsetenv("SIDECAR_IMAGE")
	os.Unsetenv("SIDECAR_IMAGE_PULL_POLICY")
	os.Unsetenv("NAMESPACE")
	os.Unsetenv("KUBE_CLUSTER_DOMAIN")
}
