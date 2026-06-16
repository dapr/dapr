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

	t.Run("ActorTypeReserved via RegisterActorTimer", func(t *testing.T) {
		_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "dapr.internal.default.myapp.workflow",
			ActorId:   "myid",
			Name:      "mytimer",
		})
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.PermissionDenied, s.Code())
		for _, detail := range s.Details() {
			if errInfo, ok := detail.(*errdetails.ErrorInfo); ok {
				require.Equal(t, "ERR_ACTOR_TYPE_RESERVED", errInfo.GetReason())
				return
			}
		}
		t.Fatal("expected ErrorInfo detail with ERR_ACTOR_TYPE_RESERVED")
	})

	t.Run("ActorTypeReserved via RegisterActorReminder", func(t *testing.T) {
		_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
			ActorType: "dapr.internal.default.myapp.workflow",
			ActorId:   "myid",
			Name:      "myreminder",
		})
		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.PermissionDenied, s.Code())
		for _, detail := range s.Details() {
			if errInfo, ok := detail.(*errdetails.ErrorInfo); ok {
				require.Equal(t, "ERR_ACTOR_TYPE_RESERVED", errInfo.GetReason())
				return
			}
		}
		t.Fatal("expected ErrorInfo detail with ERR_ACTOR_TYPE_RESERVED")
	})
}
