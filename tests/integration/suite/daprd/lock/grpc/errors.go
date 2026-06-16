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

// errors tests the rich error responses for the Distributed Lock gRPC API.
// Only errors that can be triggered without an external lock store backend are
// covered here (store-level errors require a real lock store component).
type errors struct {
	daprd *procdaprd.Daprd
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	e.daprd = procdaprd.New(t)
	return []framework.Option{
		framework.WithProcesses(e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)
	client := e.daprd.GRPCClient(t, ctx)

	t.Run("expiry in seconds not positive", func(t *testing.T) {
		_, err := client.TryLockAlpha1(ctx, &rtv1.TryLockRequest{
			StoreName:       "mystore",
			ResourceId:      "resource",
			LockOwner:       "owner",
			ExpiryInSeconds: 0,
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, "ExpiryInSeconds is not positive in lock store mystore", s.Message())
		require.Len(t, s.Details(), 1)
		errInfo, ok := s.Details()[0].(*errdetails.ErrorInfo)
		require.True(t, ok)
		require.Equal(t, "ERR_MALFORMED_REQUEST", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	t.Run("lock store not configured", func(t *testing.T) {
		_, err := client.TryLockAlpha1(ctx, &rtv1.TryLockRequest{
			StoreName:       "mystore",
			ResourceId:      "resource",
			LockOwner:       "owner",
			ExpiryInSeconds: 10,
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.FailedPrecondition, s.Code())
		require.Equal(t, "lock store is not configured", s.Message())
		require.Len(t, s.Details(), 1)
		errInfo, ok := s.Details()[0].(*errdetails.ErrorInfo)
		require.True(t, ok)
		require.Equal(t, "ERR_LOCK_STORE_NOT_CONFIGURED", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	t.Run("unlock lock store not configured", func(t *testing.T) {
		_, err := client.UnlockAlpha1(ctx, &rtv1.UnlockRequest{
			StoreName:  "mystore",
			ResourceId: "resource",
			LockOwner:  "owner",
		})
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.FailedPrecondition, s.Code())
		require.Equal(t, "lock store is not configured", s.Message())
		require.Len(t, s.Details(), 1)
		errInfo, ok := s.Details()[0].(*errdetails.ErrorInfo)
		require.True(t, ok)
		require.Equal(t, "ERR_LOCK_STORE_NOT_CONFIGURED", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})
}
