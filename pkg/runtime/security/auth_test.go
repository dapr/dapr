package security

import (
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	diag "github.com/dapr/dapr/pkg/diagnostics"
)

func mockGenCSR(id string, _ *diag.Metrics) ([]byte, []byte, error) {
	return []byte{1}, []byte{2}, nil
}

func getTestAuthenticator(t *testing.T) Authenticator {
	t.Helper()
	metrics, err := diag.NewMetrics(nil)
	require.NoError(t, err)
	return newAuthenticator("test", metrics, x509.NewCertPool(), nil, nil, mockGenCSR)
}

func TestGetTrustAuthAnchors(t *testing.T) {
	a := getTestAuthenticator(t)
	ta := a.GetTrustAnchors()
	assert.NotNil(t, ta)
}

func TestGetCurrentSignedCert(t *testing.T) {
	a := getTestAuthenticator(t)
	a.(*authenticator).currentSignedCert = &SignedCertificate{}
	c := a.GetCurrentSignedCert()
	assert.NotNil(t, c)
}

func TestGetSentryIdentifier(t *testing.T) {
	t.Run("with identity in env", func(t *testing.T) {
		envID := "cluster.local"
		t.Setenv("SENTRY_LOCAL_IDENTITY", envID)

		id := getSentryIdentifier("app1")
		assert.Equal(t, envID, id)
	})

	t.Run("without identity in env", func(t *testing.T) {
		id := getSentryIdentifier("app1")
		assert.Equal(t, "app1", id)
	})
}
