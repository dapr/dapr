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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	grpcCodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/components-contrib/secretstores"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/file"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/secretstore"
	"github.com/dapr/dapr/tests/integration/framework/process/secretstore/localfile"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	kitErrors "github.com/dapr/kit/errors"
)

func init() {
	suite.Register(new(errorcodes))
}

type errorcodes struct {
	daprd *procdaprd.Daprd
}

func (e *errorcodes) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	socket := socket.New(t)

	storeWithGetSecret := secretstore.New(t,
		secretstore.WithSocket(socket),
		secretstore.WithSecretStore(localfile.New(t,
			localfile.WithGetSecretFn(func(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
				return secretstores.GetSecretResponse{}, errors.New("get secret error")
			}),
		)),
	)

	storeWithBulkGetSecret := secretstore.New(t,
		secretstore.WithSocket(socket),
		secretstore.WithSecretStore(localfile.New(t,
			localfile.WithBulkGetSecretFn(func(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
				return secretstores.BulkGetSecretResponse{}, errors.New("bulk get secret error")
			}),
		)),
	)

	fileName := file.Paths(t, 1)[0]
	values := map[string]string{
		"mySecret":   "myKey",
		"notAllowed": "myKey",
	}
	bytes, err := json.Marshal(values)
	require.NoError(t, err)
	err = os.WriteFile(fileName, bytes, 0o600)
	require.NoError(t, err)
	configFile := file.Paths(t, 1)[0]
	err = os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: appconfig
spec:
  secrets:
    scopes:
      - storeName: mystore
        defaultAccess: allow # this is the default value, line can be omitted
        deniedSecrets: ["notAllowed"]
`), 0o600)
	require.NoError(t, err)

	e.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore-get-secret-error
spec:
 type: secretstores.%s
 version: v1  
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore-bulk-get-secret-error
spec:
 type: secretstores.%s
 version: v1
`, strings.ReplaceAll(fileName, "'", "''"), storeWithGetSecret.SocketName(), storeWithBulkGetSecret.SocketName())),
		procdaprd.WithSocket(t, socket),
		procdaprd.WithConfigs(configFile),
	)

	return []framework.Option{
		framework.WithProcesses(storeWithGetSecret, storeWithBulkGetSecret, e.daprd),
	}
}

func (e *errorcodes) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)
	client := e.daprd.GRPCClient(t, ctx)

	// Covers apierrors.SecretStore("").NotConfigured()
	t.Run("secret store not configured", func(t *testing.T) {
		// Start a new daprd without secret store
		daprdNoSecretStore := procdaprd.New(t, procdaprd.WithAppID("daprd_no_secret_store"))
		daprdNoSecretStore.Run(t, ctx)
		daprdNoSecretStore.WaitUntilRunning(t, ctx)
		defer daprdNoSecretStore.Cleanup(t)
		//nolint:staticcheck
		connNoSecretStore, err := grpc.DialContext(ctx, fmt.Sprintf("localhost:%d", daprdNoSecretStore.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, connNoSecretStore.Close()) })
		clientNoSecretStore := rtv1.NewDaprClient(connNoSecretStore)

		storeName := "mystore"
		_, err = clientNoSecretStore.GetSecret(ctx, &rtv1.GetSecretRequest{
			StoreName: storeName,
			Key:       "mysecret",
			Metadata:  nil,
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.FailedPrecondition, s.Code())
		require.Equal(t, "secret store is not configured", s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)
		errInfo := s.Details()[0]
		require.IsType(t, &errdetails.ErrorInfo{}, errInfo)
		require.Equal(t, kitErrors.CodePrefixSecretStore+kitErrors.CodeNotConfigured, errInfo.(*errdetails.ErrorInfo).GetReason())
		require.Equal(t, "dapr.io", errInfo.(*errdetails.ErrorInfo).GetDomain())
		require.Nil(t, errInfo.(*errdetails.ErrorInfo).GetMetadata())
	})

	// Covers apierrors.SecretStore("").NotFound()
	t.Run("secret store doesn't exist", func(t *testing.T) {
		storeName := "mystore-doesnt-exist"
		_, err := client.GetSecret(ctx, &rtv1.GetSecretRequest{
			StoreName: storeName,
			Key:       "mysecret",
			Metadata:  nil,
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("secret store %s not found", storeName), s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)
		errInfo := s.Details()[0]
		require.IsType(t, &errdetails.ErrorInfo{}, errInfo)
		require.Equal(t, kitErrors.CodePrefixSecretStore+kitErrors.CodeNotFound, errInfo.(*errdetails.ErrorInfo).GetReason())
		require.Equal(t, "dapr.io", errInfo.(*errdetails.ErrorInfo).GetDomain())
		require.Nil(t, errInfo.(*errdetails.ErrorInfo).GetMetadata())
	})

	// Covers apierrors.SecretStore("").PermissionDenied()
	t.Run("secret store permission denied", func(t *testing.T) {
		storeName := "mystore"
		key := "notAllowed"
		_, err := client.GetSecret(ctx, &rtv1.GetSecretRequest{
			StoreName: storeName,
			Key:       key,
			Metadata:  nil,
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.PermissionDenied, s.Code())
		require.Equal(t, fmt.Sprintf("access denied by policy to get %q from %q", key, storeName), s.Message())

		// Check status details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		var resInfo *errdetails.ResourceInfo

		for _, detail := range s.Details() {
			switch d := detail.(type) {
			case *errdetails.ErrorInfo:
				errInfo = d
			case *errdetails.ResourceInfo:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, kitErrors.CodePrefixSecretStore+"PERMISSION_DENIED", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, string(metadata.SecretStoreType), resInfo.GetResourceType())
		require.Equal(t, storeName, resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
		require.Empty(t, resInfo.GetDescription())
	})

	// Covers apierrors.SecretStore("").GetSecret()
	t.Run("secret store get secret failed", func(t *testing.T) {
		storeName := "mystore-get-secret-error"
		key := "mysecret"
		_, err := client.GetSecret(ctx, &rtv1.GetSecretRequest{
			StoreName: storeName,
			Key:       key,
			Metadata:  nil,
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.Internal, s.Code())
		require.Equal(t, fmt.Sprintf("failed getting secret with key %s from secret store %s: %s", key, storeName, "rpc error: code = Unknown desc = get secret error"), s.Message())

		// Check details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		var resInfo *errdetails.ResourceInfo

		for _, detail := range s.Details() {
			switch d := detail.(type) {
			case *errdetails.ErrorInfo:
				errInfo = d
			case *errdetails.ResourceInfo:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, kitErrors.CodePrefixSecretStore+"GET_SECRET", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, string(metadata.SecretStoreType), resInfo.GetResourceType())
		require.Equal(t, storeName, resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
		require.Empty(t, resInfo.GetDescription())
	})

	// Covers apierrors.SecretStore("").BulkSecretGet()
	t.Run("secret store get bulk secret failed", func(t *testing.T) {
		storeName := "mystore-bulk-get-secret-error"
		_, err := client.GetBulkSecret(ctx, &rtv1.GetBulkSecretRequest{
			StoreName: storeName,
			Metadata:  nil,
		})

		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.Internal, s.Code())
		require.Equal(t, fmt.Sprintf("failed getting secrets from secret store %s: %v", storeName, "rpc error: code = Unknown desc = bulk get secret error"), s.Message())

		// Check details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		var resInfo *errdetails.ResourceInfo

		for _, detail := range s.Details() {
			switch d := detail.(type) {
			case *errdetails.ErrorInfo:
				errInfo = d
			case *errdetails.ResourceInfo:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}

		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, kitErrors.CodePrefixSecretStore+"GET_BULK_SECRET", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, string(metadata.SecretStoreType), resInfo.GetResourceType())
		require.Equal(t, storeName, resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
		require.Empty(t, resInfo.GetDescription())
	})
}
