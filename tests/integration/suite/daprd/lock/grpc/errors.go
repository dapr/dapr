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
	"testing"

	apiErrors "github.com/dapr/dapr/pkg/api/errors"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	kitErrors "github.com/dapr/kit/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	grpcCodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func init() {
	suite.Register(new(standardizedErrors))
}

type standardizedErrors struct {
	daprd *daprd.Daprd
}

func (e *standardizedErrors) Setup(t *testing.T) []framework.Option {
	e.daprd = daprd.New(t,
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: lockstore
spec:
  type: lock.redis
  version: v1
  metadata:
  - name: redisHost
    value: localhost:6379
  - name: redisPassword
    value: ""
`))

	return []framework.Option{
		framework.WithProcesses(e.daprd),
	}
}

func (e *standardizedErrors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, e.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Covers errors.ERR_LOCK_STORE_NOT_FOUND
	t.Run("lock doesn't exist", func(t *testing.T) {
		name := "lock-doesn't-exist"
		req := &rtv1.TryLockRequest{
			StoreName:       name,
			ExpiryInSeconds: 10,
			ResourceId:      "resource",
			LockOwner:       "owner",
		}
		_, err := client.TryLockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("lock store %s is not found", "lock-doesn't-exist"), s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kitErrors.CodePrefixLock+kitErrors.CodeNotFound, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	// Covers errors.ERR_RESOURCE_ID_EMPTY
	t.Run("lock resource id empty", func(t *testing.T) {
		name := "lockstore"
		req := &rtv1.TryLockRequest{
			StoreName:       name,
			ExpiryInSeconds: 10,
			LockOwner:       "owner",
		}
		_, err := client.TryLockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("ResourceId is empty in lock store %s", name), s.Message())

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
		require.Equal(t, kitErrors.CodePrefixLock+apiErrors.PostFixIDEmpty, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
		require.Equal(t, "owner", resInfo.GetOwner())
	})

	// Covers errors.ERR_LOCK_OWNER_EMPTY
	t.Run("lock owner empty", func(t *testing.T) {
		name := "lockstore"
		req := &rtv1.TryLockRequest{
			StoreName:       name,
			ExpiryInSeconds: 10,
			ResourceId:      "resource",
		}
		_, err := client.TryLockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("LockOwner is empty in lock store %s", name), s.Message())

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
		require.Equal(t, kitErrors.CodePrefixLock+apiErrors.PostFixLockOwnerEmpty, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
	})

	// Covers errors.ERR_EXPIRY_NOT_POSITIVE
	t.Run("lock expiry in seconds not positive", func(t *testing.T) {
		name := "lockstore"
		req := &rtv1.TryLockRequest{
			StoreName:       name,
			ExpiryInSeconds: -1,
			ResourceId:      "resource",
			LockOwner:       "owner",
		}
		_, err := client.TryLockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("ExpiryInSeconds is not positive in lock store %s", name), s.Message())

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
		require.Equal(t, kitErrors.CodePrefixLock+apiErrors.PostFixExpiryInSecondsNotPositive, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
		require.Equal(t, "owner", resInfo.GetOwner())
	})

	// Covers errors.ERR_LOCK_STORE_NOT_CONFIGURED
	t.Run("lock store not configured", func(t *testing.T) {
		// Start a new daprd without lock store
		daprdNoLockStore := daprd.New(t, daprd.WithAppID("daprd_no_lock_store"))
		daprdNoLockStore.Run(t, ctx)
		daprdNoLockStore.WaitUntilRunning(t, ctx)
		defer daprdNoLockStore.Cleanup(t)
		connNoLockStore, err := grpc.DialContext(ctx, fmt.Sprintf("localhost:%d", daprdNoLockStore.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, connNoLockStore.Close()) })
		clientNoLockStore := rtv1.NewDaprClient(connNoLockStore)

		name := "lockstore"
		req := &rtv1.TryLockRequest{
			StoreName:       name,
			ExpiryInSeconds: 10,
			ResourceId:      "resource",
			LockOwner:       "owner",
		}
		_, err = clientNoLockStore.TryLockAlpha1(ctx, req)
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.FailedPrecondition, s.Code())
		require.Equal(t, fmt.Sprintf("lock store %s is not configured", name), s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)
		errInfo := s.Details()[0]
		require.IsType(t, &errdetails.ErrorInfo{}, errInfo)
		require.Equal(t, "DAPR_LOCK_NOT_CONFIGURED", errInfo.(*errdetails.ErrorInfo).GetReason())
		require.Equal(t, "dapr.io", errInfo.(*errdetails.ErrorInfo).GetDomain())
		require.Nil(t, errInfo.(*errdetails.ErrorInfo).GetMetadata())
	})

	// Covers errors.ERR_TRY_LOCK
	t.Run("try lock failed", func(t *testing.T) {
		name := "lockstore"
		resourceID := "resource||"
		req := &rtv1.TryLockRequest{
			StoreName:       name,
			ExpiryInSeconds: 10,
			ResourceId:      resourceID,
			LockOwner:       "owner",
		}
		_, err := client.TryLockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.Internal, s.Code())
		t.Log(err.Error())
		// checkKeyIllegal error
		require.Equal(t, fmt.Sprintf("failed to try acquiring lock: input key/keyPrefix '%s' can't contain '%s'", resourceID, "||"), s.Message())

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
		require.Equal(t, kitErrors.CodePrefixLock+apiErrors.PostFixTryLock, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
		require.Equal(t, "owner", resInfo.GetOwner())
	})

	// Covers errors.ERR_Unlock
	t.Run("unlock failed", func(t *testing.T) {
		name := "lockstore"
		resourceID := "resource||"
		req := &rtv1.UnlockRequest{
			StoreName:  name,
			ResourceId: resourceID,
			LockOwner:  "owner",
		}
		_, err := client.UnlockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.Internal, s.Code())
		t.Log(err.Error())
		// checkKeyIllegal error
		require.Equal(t, fmt.Sprintf("failed to release lock: input key/keyPrefix '%s' can't contain '%s'", resourceID, "||"), s.Message())

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
		require.Equal(t, kitErrors.CodePrefixLock+apiErrors.PostFixUnlock, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
		require.Equal(t, "owner", resInfo.GetOwner())
	})
}
