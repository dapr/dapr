package injector

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetInjectorConfig(t *testing.T) {
	t.Run("with kube cluster domain env", func(t *testing.T) {
		t.Setenv("TLS_CERT_FILE", "test-cert-file")
		t.Setenv("TLS_KEY_FILE", "test-key-file")
		t.Setenv("SIDECAR_IMAGE", "daprd-test-image")
		t.Setenv("SIDECAR_IMAGE_PULL_POLICY", "Always")
		t.Setenv("NAMESPACE", "test-namespace")
		t.Setenv("KUBE_CLUSTER_DOMAIN", "cluster.local")
		t.Setenv("ALLOWED_SERVICE_ACCOUNTS", "test-service-account1:test1,test-service-account2:test2")

		cfg, err := GetConfig()
		assert.Nil(t, err)
		assert.Equal(t, "test-cert-file", cfg.TLSCertFile)
		assert.Equal(t, "test-key-file", cfg.TLSKeyFile)
		assert.Equal(t, "daprd-test-image", cfg.SidecarImage)
		assert.Equal(t, "Always", cfg.SidecarImagePullPolicy)
		assert.Equal(t, "test-namespace", cfg.Namespace)
		assert.Equal(t, "cluster.local", cfg.KubeClusterDomain)
		assert.Equal(t, "test-service-account1:test1,test-service-account2:test2", cfg.AllowedServiceAccounts)
	})

	t.Run("not set kube cluster domain env", func(t *testing.T) {
		t.Setenv("TLS_CERT_FILE", "test-cert-file")
		t.Setenv("TLS_KEY_FILE", "test-key-file")
		t.Setenv("SIDECAR_IMAGE", "daprd-test-image")
		t.Setenv("SIDECAR_IMAGE_PULL_POLICY", "IfNotPresent")
		t.Setenv("NAMESPACE", "test-namespace")
		t.Setenv("KUBE_CLUSTER_DOMAIN", "")

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
