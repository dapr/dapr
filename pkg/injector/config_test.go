package injector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
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

func TestImagePullPolicy(t *testing.T) {
	testCases := []struct {
		testName       string
		pullPolicy     string
		expectedPolicy corev1.PullPolicy
	}{
		{
			"TestDefaultPullPolicy",
			"",
			corev1.PullIfNotPresent,
		},
		{
			"TestAlwaysPullPolicy",
			"Always",
			corev1.PullAlways,
		},
		{
			"TestNeverPullPolicy",
			"Never",
			corev1.PullNever,
		},
		{
			"TestIfNotPresentPullPolicy",
			"IfNotPresent",
			corev1.PullIfNotPresent,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			c := NewConfigWithDefaults()
			c.SidecarImagePullPolicy = tc.pullPolicy
			assert.Equal(t, tc.expectedPolicy, c.GetPullPolicy())
		})
	}
}

func TestTolerationsParsing(t *testing.T) {
	testCases := []struct {
		name   string
		expect []corev1.Toleration
		input  string
	}{
		{
			"empty tolerations",
			[]corev1.Toleration{},
			"",
		},
		{
			"single toleration",
			[]corev1.Toleration{
				{
					Key:    "foo.com/bar",
					Effect: "NoSchedule",
				},
			},
			`[{"key":"foo.com/bar","Effect":"NoSchedule"}]`,
		},
		{
			"multiple tolerations",
			[]corev1.Toleration{
				{
					Key:    "foo.com/bar",
					Effect: "NoSchedule",
				},
				{
					Key:      "foo.com/baz",
					Operator: "Equal",
					Value:    "foobar",
					Effect:   "NoSchedule",
				},
			},
			`[{"key":"foo.com/bar","Effect":"NoSchedule"},{"key":"foo.com/baz","Operator":"Equal","Value":"foobar","Effect":"NoSchedule"}]`,
		},
		{
			"invalid JSON",
			[]corev1.Toleration{},
			`hi`,
		},
		{
			"invalid JSON structure",
			[]corev1.Toleration{},
			`{}`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{
				IgnoreEntrypointTolerations: tc.input,
			}
			c.parseTolerationsJSON()
			assert.EqualValues(t, tc.expect, c.GetIgnoreEntrypointTolerations())
		})
	}
}
