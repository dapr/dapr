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
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	grpcCodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	kitErrors "github.com/dapr/kit/errors"
)

func init() {
	suite.Register(new(errorcodes))
}

type errorcodes struct {
	daprd *daprd.Daprd
}

func (e *errorcodes) Setup(t *testing.T) []framework.Option {
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

func (e *errorcodes) Run(t *testing.T, ctx context.Context) {
	const LockStoreName = "lockstore"

	e.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.DialContext(ctx, e.daprd.GRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Covers apierrors.ERR_LOCK_STORE_NOT_FOUND
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
		require.Equal(t, fmt.Sprintf("lock %s is not found", "lock-doesn't-exist"), s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kitErrors.CodePrefixLock+kitErrors.CodeNotFound, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata(), "Metadata should be present")
	})

	// Covers apierrors.ERR_LOCK_STORE_NOT_CONFIGURED
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

		req := &rtv1.TryLockRequest{
			StoreName:       LockStoreName,
			ExpiryInSeconds: 10,
			ResourceId:      "resource",
			LockOwner:       "owner",
		}
		_, err = clientNoLockStore.TryLockAlpha1(ctx, req)
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.FailedPrecondition, s.Code())
		require.Equal(t, fmt.Sprintf("lock %s is not configured", LockStoreName), s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)
		errInfo := s.Details()[0]
		require.IsType(t, &errdetails.ErrorInfo{}, errInfo)
		require.Equal(t, "DAPR_LOCK_NOT_CONFIGURED", errInfo.(*errdetails.ErrorInfo).GetReason())
		require.Equal(t, "dapr.io", errInfo.(*errdetails.ErrorInfo).GetDomain())
		require.NotNil(t, errInfo.(*errdetails.ErrorInfo).GetMetadata(), "Metadata should be present")
	})

	// Covers apierrors.ERR_MALFORMED_REQUEST
	t.Run("lock resource id empty", func(t *testing.T) {
		req := &rtv1.TryLockRequest{
			StoreName:       LockStoreName,
			ExpiryInSeconds: 10,
			LockOwner:       "owner",
		}
		_, err := client.TryLockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, "lock resource id is empty", s.Message())

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
		require.Equal(t, "DAPR_LOCK_RESOURCE_ID_EMPTY", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata(), "Metadata should be present")

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
		require.Equal(t, "owner", resInfo.GetOwner())
	})

	// Covers apierrors.ERR_LOCK_OWNER_EMPTY
	t.Run("lock owner empty", func(t *testing.T) {
		req := &rtv1.TryLockRequest{
			StoreName:       LockStoreName,
			ExpiryInSeconds: 10,
			ResourceId:      "resource",
		}
		_, err := client.TryLockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, "lock owner is empty", s.Message())

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
		require.Equal(t, "DAPR_LOCK_OWNER_EMPTY", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata(), "Metadata should be present")

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
	})

	// Covers apierrors.ERR_MALFORMED_REQUEST
	t.Run("lock expiry in seconds not positive", func(t *testing.T) {
		req := &rtv1.TryLockRequest{
			StoreName:       LockStoreName,
			ExpiryInSeconds: -1,
			ResourceId:      "resource",
			LockOwner:       "owner",
		}
		_, err := client.TryLockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, "expiry in seconds is not positive", s.Message())

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
		require.Equal(t, "DAPR_LOCK_EXPIRY_NOT_POSITIVE", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata(), "Metadata should be present")

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
		require.Equal(t, "owner", resInfo.GetOwner())
	})

	// Covers apierrors.ERR_TRY_LOCK
	t.Run("try lock failed", func(t *testing.T) {
		resourceID := "resource||"
		req := &rtv1.TryLockRequest{
			StoreName:       LockStoreName,
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
		require.Equal(t, "failed to try acquiring lock", s.Message())

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
		require.Equal(t, "DAPR_LOCK_TRY_LOCK_FAILED", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata(), "Metadata should be present")

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
		require.Equal(t, "owner", resInfo.GetOwner())
	})

	// Covers apierrors.ERR_Unlock
	t.Run("unlock failed", func(t *testing.T) {
		resourceID := "resource||"
		req := &rtv1.UnlockRequest{
			StoreName:  LockStoreName,
			ResourceId: resourceID,
			LockOwner:  "owner",
		}
		_, err := client.UnlockAlpha1(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.Internal, s.Code())
		t.Log(err.Error())
		require.Equal(t, "failed to release lock", s.Message())

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
		require.Equal(t, "DAPR_LOCK_UNLOCK_FAILED", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata(), "Metadata should be present")

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "lock", resInfo.GetResourceType())
		require.Equal(t, "lockstore", resInfo.GetResourceName())
		require.Equal(t, "owner", resInfo.GetOwner())
	})
}
