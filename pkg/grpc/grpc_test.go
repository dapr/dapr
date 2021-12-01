/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"crypto/x509"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/connectivity"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/security"
)

type authenticatorMock struct{}

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

func TestGetGRPCConnection(t *testing.T) {
	t.Run("Connection is closed", func(t *testing.T) {
		m := NewGRPCManager(modes.StandaloneMode)
		assert.NotNil(t, m)
		port := 55555
		sslEnabled := false
		ctx := context.TODO()
		conn, err := m.GetGRPCConnection(ctx, fmt.Sprintf("127.0.0.1:%v", port), "", "", true, true, sslEnabled)
		assert.NoError(t, err)
		conn2, err2 := m.GetGRPCConnection(ctx, fmt.Sprintf("127.0.0.1:%v", port), "", "", true, true, sslEnabled)
		assert.NoError(t, err2)
		assert.Equal(t, connectivity.Shutdown, conn.GetState())
		conn2.Close()
	})

	t.Run("Connection with SSL is created successfully", func(t *testing.T) {
		m := NewGRPCManager(modes.StandaloneMode)
		assert.NotNil(t, m)
		port := 55555
		sslEnabled := true
		ctx := context.TODO()
		_, err := m.GetGRPCConnection(ctx, fmt.Sprintf("127.0.0.1:%v", port), "", "", true, true, sslEnabled)
		assert.NoError(t, err)
	})
}

func TestSetAuthenticator(t *testing.T) {
	a := &authenticatorMock{}
	m := NewGRPCManager(modes.StandaloneMode)
	m.SetAuthenticator(a)

	assert.Equal(t, a, m.auth)
}
