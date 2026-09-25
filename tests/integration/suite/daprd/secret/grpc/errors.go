/*
Copyright 2024 The Dapr Authors
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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	grpcCodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprdNoStore   *procdaprd.Daprd
	daprdWithStore *procdaprd.Daprd
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	secretFile := filepath.Join(t.TempDir(), "secrets.json")
	require.NoError(t, os.WriteFile(secretFile, []byte(`{"mykey":"myvalue"}`), 0o600))

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: myconfig
spec:
  secrets:
    scopes:
    - storeName: mysecretstore
      defaultAccess: deny
      allowedSecrets: ["mykey"]
`), 0o600))

	e.daprdNoStore = procdaprd.New(t)
	e.daprdWithStore = procdaprd.New(t,
		procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mysecretstore
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
`, secretFile)),
		procdaprd.WithConfigs(configFile),
	)

	return []framework.Option{
		framework.WithProcesses(e.daprdNoStore, e.daprdWithStore),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprdNoStore.WaitUntilRunning(t, ctx)
	e.daprdWithStore.WaitUntilRunning(t, ctx)

	clientNoStore := e.daprdNoStore.GRPCClient(t, ctx)
	clientWithStore := e.daprdWithStore.GRPCClient(t, ctx)

	t.Run("secret store not configured", func(t *testing.T) {
		_, err := clientNoStore.GetSecret(ctx, &rtv1.GetSecretRequest{
			StoreName: "mysecretstore",
			Key:       "mykey",
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.FailedPrecondition, s.Code())
		require.Equal(t, "secret store is not configured", s.Message())
		require.Len(t, s.Details(), 1)
		errInfo, ok := s.Details()[0].(*errdetails.ErrorInfo)
		require.True(t, ok)
		require.Equal(t, "ERR_SECRET_STORES_NOT_CONFIGURED", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	t.Run("secret store not found", func(t *testing.T) {
		_, err := clientWithStore.GetSecret(ctx, &rtv1.GetSecretRequest{
			StoreName: "nonexistent-store",
			Key:       "mykey",
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, "failed finding secret store with key nonexistent-store", s.Message())
		require.Len(t, s.Details(), 1)
		errInfo, ok := s.Details()[0].(*errdetails.ErrorInfo)
		require.True(t, ok)
		require.Equal(t, "ERR_SECRET_STORE_NOT_FOUND", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	t.Run("secret permission denied", func(t *testing.T) {
		_, err := clientWithStore.GetSecret(ctx, &rtv1.GetSecretRequest{
			StoreName: "mysecretstore",
			Key:       "denied-key",
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.PermissionDenied, s.Code())
		require.Equal(t, `access denied by policy to get "denied-key" from "mysecretstore"`, s.Message())
		require.Len(t, s.Details(), 1)
		errInfo, ok := s.Details()[0].(*errdetails.ErrorInfo)
		require.True(t, ok)
		require.Equal(t, "ERR_PERMISSION_DENIED", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})
}
