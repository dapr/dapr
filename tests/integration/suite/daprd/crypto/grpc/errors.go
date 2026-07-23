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

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprdNoProvider   *procdaprd.Daprd
	daprdWithProvider *procdaprd.Daprd
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	keyDir := t.TempDir()
	pk := make([]byte, 32)
	for i := range pk {
		pk[i] = byte(i)
	}
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, "mykey"), pk, 0o600))

	e.daprdNoProvider = procdaprd.New(t)
	e.daprdWithProvider = procdaprd.New(t,
		procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mycrypto
spec:
  type: crypto.dapr.localstorage
  version: v1
  metadata:
  - name: path
    value: '%s'
`, keyDir)),
	)

	return []framework.Option{
		framework.WithProcesses(e.daprdNoProvider, e.daprdWithProvider),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprdNoProvider.WaitUntilRunning(t, ctx)
	e.daprdWithProvider.WaitUntilRunning(t, ctx)

	t.Run("ProvidersNotConfigured", func(t *testing.T) {
		client := e.daprdNoProvider.GRPCClient(t, ctx)

		stream, err := client.EncryptAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&rtv1.EncryptRequest{
			Options: &rtv1.EncryptRequestOptions{
				ComponentName:    "mycrypto",
				KeyName:          "mykey",
				KeyWrapAlgorithm: "AES",
			},
			Payload: &commonv1.StreamPayload{Data: []byte("hello"), Seq: 0},
		}))
		_, err = stream.Recv()
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.Internal, s.Code())
		for _, detail := range s.Details() {
			if errInfo, ok := detail.(*errdetails.ErrorInfo); ok {
				require.Equal(t, "ERR_CRYPTO_PROVIDERS_NOT_CONFIGURED", errInfo.GetReason())
				return
			}
		}
		t.Fatal("expected ErrorInfo detail with ERR_CRYPTO_PROVIDERS_NOT_CONFIGURED")
	})

	t.Run("ProviderNotFound", func(t *testing.T) {
		client := e.daprdWithProvider.GRPCClient(t, ctx)

		stream, err := client.EncryptAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&rtv1.EncryptRequest{
			Options: &rtv1.EncryptRequestOptions{
				ComponentName:    "nonexistent",
				KeyName:          "mykey",
				KeyWrapAlgorithm: "AES",
			},
			Payload: &commonv1.StreamPayload{Data: []byte("hello"), Seq: 0},
		}))
		_, err = stream.Recv()
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		for _, detail := range s.Details() {
			if errInfo, ok := detail.(*errdetails.ErrorInfo); ok {
				require.Equal(t, "ERR_CRYPTO_PROVIDER_NOT_FOUND", errInfo.GetReason())
				return
			}
		}
		t.Fatal("expected ErrorInfo detail with ERR_CRYPTO_PROVIDER_NOT_FOUND")
	})

	t.Run("BadRequest missing options", func(t *testing.T) {
		client := e.daprdWithProvider.GRPCClient(t, ctx)

		stream, err := client.EncryptAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&rtv1.EncryptRequest{
			Payload: &commonv1.StreamPayload{Data: []byte("hello"), Seq: 0},
		}))
		_, err = stream.Recv()
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		for _, detail := range s.Details() {
			if errInfo, ok := detail.(*errdetails.ErrorInfo); ok {
				require.Equal(t, "ERR_BAD_REQUEST", errInfo.GetReason())
				return
			}
		}
		t.Fatal("expected ErrorInfo detail with ERR_BAD_REQUEST")
	})

	t.Run("BadRequest missing keyName", func(t *testing.T) {
		client := e.daprdWithProvider.GRPCClient(t, ctx)

		stream, err := client.EncryptAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&rtv1.EncryptRequest{
			Options: &rtv1.EncryptRequestOptions{
				ComponentName:    "mycrypto",
				KeyWrapAlgorithm: "AES",
			},
			Payload: &commonv1.StreamPayload{Data: []byte("hello"), Seq: 0},
		}))
		_, err = stream.Recv()
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		for _, detail := range s.Details() {
			if errInfo, ok := detail.(*errdetails.ErrorInfo); ok {
				require.Equal(t, "ERR_BAD_REQUEST", errInfo.GetReason())
				return
			}
		}
		t.Fatal("expected ErrorInfo detail with ERR_BAD_REQUEST")
	})

	t.Run("BadRequest missing keyWrapAlgorithm", func(t *testing.T) {
		client := e.daprdWithProvider.GRPCClient(t, ctx)

		stream, err := client.EncryptAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&rtv1.EncryptRequest{
			Options: &rtv1.EncryptRequestOptions{
				ComponentName: "mycrypto",
				KeyName:       "mykey",
			},
			Payload: &commonv1.StreamPayload{Data: []byte("hello"), Seq: 0},
		}))
		_, err = stream.Recv()
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		for _, detail := range s.Details() {
			if errInfo, ok := detail.(*errdetails.ErrorInfo); ok {
				require.Equal(t, "ERR_BAD_REQUEST", errInfo.GetReason())
				return
			}
		}
		t.Fatal("expected ErrorInfo detail with ERR_BAD_REQUEST")
	})
}
