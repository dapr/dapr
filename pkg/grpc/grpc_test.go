// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"crypto/x509"
	"testing"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/stretchr/testify/assert"
)

type authenticatorMock struct {
}

func (a *authenticatorMock) GetTrustAnchors() *x509.CertPool {
	return nil
}
func (a *authenticatorMock) GetCurrentSignedCert() *security.SignedCertificate {
	return nil
}
func (a *authenticatorMock) CreateSignedWorkloadCert(id, namespace, trustDomain string) (*security.SignedCertificate, error) {
	return nil, nil
}

func TestNewGRPCManager(t *testing.T) {
	t.Run("with self hosted", func(t *testing.T) {
		m := NewGRPCManager(modes.StandaloneMode)
		assert.NotNil(t, m)
		assert.Equal(t, modes.StandaloneMode, m.mode)
	})

	t.Run("with kubernetes", func(t *testing.T) {
		m := NewGRPCManager(modes.KubernetesMode)
		assert.NotNil(t, m)
		assert.Equal(t, modes.KubernetesMode, m.mode)
	})
}

func TestSetAuthenticator(t *testing.T) {
	a := &authenticatorMock{}
	m := NewGRPCManager(modes.StandaloneMode)
	m.SetAuthenticator(a)

	assert.Equal(t, a, m.auth)
}
