/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
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

	"golang.org/x/net/nettest"
	"google.golang.org/grpc/status"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	apierrors "github.com/dapr/dapr/pkg/api/errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	grpcCodes "google.golang.org/grpc/codes"

	"github.com/dapr/components-contrib/state"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd *procdaprd.Daprd

	queryErr func(*testing.T) error
	// tooManyTransactionalOpsErr func(*testing.T) error
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	// Darwin enforces a maximum 104 byte socket name limit, so we need to be a
	// bit fancy on how we generate the name.
	tmp, err := nettest.LocalPath()
	require.NoError(t, err)

	socketDir := filepath.Join(tmp, util.RandomString(t, 4))
	require.NoError(t, os.MkdirAll(socketDir, 0o700))
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(socketDir))
	})

	e.queryErr = func(t *testing.T) error {
		require.FailNow(t, "query should not be called")
		return nil
	}

	storeWithQuerier := statestore.New(t,
		statestore.WithSocketDirectory(socketDir),
		statestore.WithStateStore(inmemory.NewQuerier(t,
			inmemory.WithQueryFn(func(context.Context, *state.QueryRequest) (*state.QueryResponse, error) {
				return nil, e.queryErr(t)
			}),
		)),
	)
	storeWithMultiMaxSize := statestore.New(t,
		statestore.WithSocketDirectory(socketDir),
		statestore.WithStateStore(inmemory.NewTransactionalMultiMaxSize(t,
			inmemory.WithTransactionalStoreMultiMaxSizeFn(func() int {
				return 1
			}),
		)),
	)

	e.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore-pluggable-querier
spec:
  type: state.%s
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore-pluggable-multimaxsize
spec:
  type: state.%s
  version: v1
`, storeWithQuerier.SocketName(), storeWithMultiMaxSize.SocketName())),
		procdaprd.WithExecOptions(exec.WithEnvVars(
			"DAPR_COMPONENTS_SOCKETS_FOLDER", socketDir,
		)),
	)

	return []framework.Option{
		framework.WithProcesses(storeWithQuerier, storeWithMultiMaxSize, e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	conn, connErr := grpc.DialContext(ctx, fmt.Sprintf("localhost:%d", e.daprd.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, connErr)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Covers errutils.StateStoreNotFound()
	t.Run("state store doesn't exist", func(t *testing.T) {
		req := &rtv1.SaveStateRequest{
			StoreName: "mystore-doesnt-exist",
			States:    []*commonv1.StateItem{{Value: []byte("value1")}},
		}
		_, err := client.SaveState(ctx, req)
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("state store %s is not found", "mystore-doesnt-exist"), s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, "DAPR_STATE_NOT_FOUND", errInfo.GetReason())
		require.Equal(t, framework.Domain, errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())
	})

	// Covers errutils.StateStoreInvalidKeyName()
	t.Run("invalid key name", func(t *testing.T) {
		keyName := "invalid||key"

		req := &rtv1.SaveStateRequest{
			StoreName: "mystore",
			States:    []*commonv1.StateItem{{Key: keyName, Value: []byte("value1")}},
		}
		_, err := client.SaveState(ctx, req)
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("input key/keyPrefix '%s' can't contain '||'", keyName), s.Message())

		// Check status details
		require.Len(t, s.Details(), 3)

		var errInfo *errdetails.ErrorInfo
		var resInfo *errdetails.ResourceInfo
		var badRequest *errdetails.BadRequest

		for _, detail := range s.Details() {
			switch d := detail.(type) {
			case *errdetails.ErrorInfo:
				errInfo = d
			case *errdetails.ResourceInfo:
				resInfo = d
			case *errdetails.BadRequest:
				badRequest = d
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, "DAPR_STATE_ILLEGAL_KEY", errInfo.GetReason())
		require.Equal(t, framework.Domain, errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "state", resInfo.GetResourceType())
		require.Equal(t, "mystore", resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
		require.Empty(t, resInfo.GetDescription())

		require.NotNil(t, badRequest, "BadRequest_FieldViolation should be present")
		require.Equal(t, keyName, badRequest.GetFieldViolations()[0].GetField())
		require.Equal(t, fmt.Sprintf("input key/keyPrefix '%s' can't contain '||'", keyName), badRequest.GetFieldViolations()[0].GetDescription())
	})

	// Covers errutils.StateStoreNotConfigured()
	t.Run("state store not configured", func(t *testing.T) {
		// Start a new daprd without state store
		daprdNoStateStore := procdaprd.New(t, procdaprd.WithAppID("daprd_no_state_store"))
		daprdNoStateStore.Run(t, ctx)
		daprdNoStateStore.WaitUntilRunning(t, ctx)
		defer daprdNoStateStore.Cleanup(t)

		connNoStateStore, err := grpc.DialContext(ctx, fmt.Sprintf("localhost:%d", daprdNoStateStore.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, connNoStateStore.Close()) })
		clientNoStateStore := rtv1.NewDaprClient(connNoStateStore)

		req := &rtv1.SaveStateRequest{
			StoreName: "mystore",
			States:    []*commonv1.StateItem{{Value: []byte("value1")}},
		}
		_, err = clientNoStateStore.SaveState(ctx, req)
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.FailedPrecondition, s.Code())
		require.Equal(t, "state store is not configured", s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)
		errInfo := s.Details()[0]
		require.IsType(t, &errdetails.ErrorInfo{}, errInfo)
		require.Equal(t, "DAPR_STATE_NOT_CONFIGURED", errInfo.(*errdetails.ErrorInfo).GetReason())
		require.Equal(t, framework.Domain, errInfo.(*errdetails.ErrorInfo).GetDomain())
		require.Nil(t, errInfo.(*errdetails.ErrorInfo).GetMetadata())
	})

	// Covers errors.StateStoreQueryUnsupported()
	t.Run("state store doesn't support query api", func(t *testing.T) {
		req := &rtv1.QueryStateRequest{
			StoreName: "mystore",
			Query:     `{"filter":{"EQ":{"state":"CA"}},"sort":[{"key":"person.id","order":"DESC"}]}`,
		}
		_, err := client.QueryStateAlpha1(ctx, req)
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.Internal, s.Code())
		require.Equal(t, "state store does not support querying", s.Message())

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
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, "DAPR_STATE_QUERYING_NOT_SUPPORTED", errInfo.GetReason())
		require.Equal(t, framework.Domain, errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "state", resInfo.GetResourceType())
		require.Equal(t, "mystore", resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
		require.Empty(t, resInfo.GetDescription())
	})

	// Covers errutils.NewErrStateStoreQueryFailed()
	t.Run("state store query failed", func(t *testing.T) {
		stateStoreName := "mystore-pluggable-querier"
		t.Cleanup(func() {
			e.queryErr = func(t *testing.T) error {
				require.FailNow(t, "query should not be called")
				return nil
			}
		})

		e.queryErr = func(*testing.T) error {
			return apierrors.StateStoreQueryFailed(stateStoreName, "this is a custom error string")
		}

		req := &rtv1.QueryStateRequest{
			StoreName: stateStoreName,
			Query:     `{"filter":{"EQ":{"state":"CA"}},"sort":[{"key":"person.id","order":"DESC"}]}`,
		}
		_, err := client.QueryStateAlpha1(ctx, req)

		require.Error(t, err)

		s, ok := status.FromError(err)

		require.True(t, ok)
		require.Equal(t, grpcCodes.Internal, s.Code())
		assert.Contains(t, s.Message(), fmt.Sprintf("state store %s query failed: this is a custom error string", stateStoreName))

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
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, "DAPR_STATE_QUERY_FAILED", errInfo.GetReason())
		require.Equal(t, framework.Domain, errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "state", resInfo.GetResourceType())
		require.Equal(t, stateStoreName, resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
		require.Empty(t, resInfo.GetDescription())
	})

	// TODO: test for NewErrStateStoreTransactionsNotSupported

	//  t.Run("state store too many transactional operations", func(t *testing.T) {
	//	t.Cleanup(func() {
	//		e.tooManyTransactionalOpsErr = func(t *testing.T) error {
	//			require.FailNow(t, "too many transactional operations")
	//			return nil
	//		}
	//	})
	//
	//	e.tooManyTransactionalOpsErr = func(*testing.T) error {
	//		return apierrors.StateStoreTooManyTransactionalOps(2, 1)
	//	}
	//
	//	ops := make([]*rtv1.TransactionalStateOperation, 0)
	//	ops = append(ops, &rtv1.TransactionalStateOperation{
	//		OperationType: "upsert",
	//		Request: &commonv1.StateItem{
	//			Key:   "key1",
	//			Value: []byte("val1"),
	//		},
	//	})
	//	ops = append(ops, &rtv1.TransactionalStateOperation{
	//		OperationType: "delete",
	//		Request: &commonv1.StateItem{
	//			Key: "key2",
	//		},
	//	})
	//	req := &rtv1.ExecuteStateTransactionRequest{
	//		StoreName:  "mystore-pluggable-multimaxsize",
	//		Operations: ops,
	//	}
	//	_, err := client.ExecuteStateTransaction(ctx, req)
	//	require.Error(t, err)
	//
	//	s, ok := status.FromError(err)
	//
	//	require.True(t, ok)
	//	require.Equal(t, grpcCodes.InvalidArgument, s.Code())
	//	assert.Equal(t, fmt.Sprintf("the transaction contains %d operations, which is more than what the state store supports: %d", 2, 1), s.Message())
	//
	//	//Check status details
	//	require.Equal(t, 1, len(s.Details()))
	//	errInfo := s.Details()[0]
	//	require.IsType(t, &errdetails.ErrorInfo{}, errInfo)
	//	require.Equal(t, "DAPR_STATE_QUERY_FAILED", errInfo.(*errdetails.ErrorInfo).GetReason())
	//	require.Equal(t, framework.Domain, errInfo.(*errdetails.ErrorInfo).GetDomain())
	//	require.Nil(t, errInfo.(*errdetails.ErrorInfo).GetMetadata())
	//  })
}
