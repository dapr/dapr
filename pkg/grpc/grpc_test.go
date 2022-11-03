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

func (a *authenticatorMock) CreateSignedWorkloadCert(ctx context.Context,
	id, namespace, trustDomain string,
) (*security.SignedCertificate, error) {
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
		recreateIfExists := true
		sslEnabled := false

		conn, teardown, err := m.GetGRPCConnection(context.TODO(), fmt.Sprintf("127.0.0.1:%v", port), "", "", true, recreateIfExists, sslEnabled)
		assert.NoError(t, err)

		_, teardown2, err2 := m.GetGRPCConnection(context.TODO(), fmt.Sprintf("127.0.0.1:%v", port), "", "", true, recreateIfExists, sslEnabled)
		assert.NoError(t, err2)
		defer teardown2()

		assert.NotEqual(t, connectivity.Shutdown, conn.GetState(), "old connection should not be closed by recreation")
		teardown()
		assert.Equal(t, connectivity.Shutdown, conn.GetState(), "old connection should be closed by teardown")
	})

	t.Run("Shared pool connection is not closed until all callers finish using it", func(t *testing.T) {
		m := NewGRPCManager(modes.StandaloneMode)
		assert.NotNil(t, m)
		port := 55555
		recreateIfExists := false
		sslEnabled := false

		conn, teardown, err := m.GetGRPCConnection(context.TODO(), fmt.Sprintf("127.0.0.1:%v", port), "", "", true, recreateIfExists, sslEnabled)
		assert.NoError(t, err)

		conn2, teardown2, err2 := m.GetGRPCConnection(context.TODO(), fmt.Sprintf("127.0.0.1:%v", port), "", "", true, recreateIfExists, sslEnabled)
		assert.NoError(t, err2)
		// conn2 is same as conn because it is stored in connection pool
		assert.Equal(t, conn, conn2)

		recreateIfExists = true
		_, teardown3, err3 := m.GetGRPCConnection(context.TODO(), fmt.Sprintf("127.0.0.1:%v", port), "", "", true, recreateIfExists, sslEnabled)
		assert.NoError(t, err3)
		defer teardown3()

		teardown()
		// connection is still used by conn2
		assert.NotEqual(t, connectivity.Shutdown, conn.GetState(), "connection must not be closed")
		teardown2()
		// connection is not used anymore
		assert.Equal(t, connectivity.Shutdown, conn.GetState(), "connection must be closed")
	})

	t.Run("Connection with SSL is created successfully", func(t *testing.T) {
		m := NewGRPCManager(modes.StandaloneMode)
		assert.NotNil(t, m)
		port := 55555
		sslEnabled := true
		ctx := context.TODO()
		_, teardown, err := m.GetGRPCConnection(ctx, fmt.Sprintf("127.0.0.1:%v", port), "", "", true, true, sslEnabled)
		assert.NoError(t, err)
		teardown()
	})
}

func TestSetAuthenticator(t *testing.T) {
	a := &authenticatorMock{}
	m := NewGRPCManager(modes.StandaloneMode)
	m.SetAuthenticator(a)

	assert.Equal(t, a, m.auth)
}
