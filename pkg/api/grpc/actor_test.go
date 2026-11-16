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
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/api/universal"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func TestGetActorState(t *testing.T) {
	t.Run("Get actor state - OK", func(t *testing.T) {
		data := []byte(`{ "data": 123 }`)

		actors := actorsfake.New()
		actors.WithState(func(context.Context) (state.Interface, error) {
			return statefake.New().WithGetFn(func(ctx context.Context, req *actorsapi.GetStateRequest, _ bool) (*actorsapi.StateResponse, error) {
				return &actorsapi.StateResponse{
					Data: data,
					Metadata: map[string]string{
						"ttlExpireTime": "2020-10-02T22:00:00Z",
					},
				}, nil
			}), nil
		})

		api := &api{
			Universal: universal.New(universal.Options{
				AppID:  "fakeAPI",
				Actors: actors,
			}),
		}
		lis := startDaprAPIServer(t, api, "")

		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)

		// act
		res, err := client.GetActorState(t.Context(), &runtimev1pb.GetActorStateRequest{
			ActorId:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		})

		// assert
		require.NoError(t, err)
		assert.Equal(t, data, res.GetData())
		assert.Equal(t, map[string]string{
			"ttlExpireTime": "2020-10-02T22:00:00Z",
		}, res.GetMetadata())
	})
}

func TestExecuteActorStateTransaction(t *testing.T) {
	t.Run("Save actor state - Upsert and Delete OK", func(t *testing.T) {
		data := []byte("{ \"data\": 123 }")

		actors := actorsfake.New()
		actors.WithState(func(context.Context) (state.Interface, error) {
			return statefake.New().WithTransactionalStateOperationFn(func(ctx context.Context, _ bool, req *actorsapi.TransactionalRequest, _ bool) error {
				return nil
			}), nil
		})

		api := &api{
			Universal: universal.New(universal.Options{
				AppID:  "fakeAPI",
				Actors: actors,
			}),
		}

		lis := startDaprAPIServer(t, api, "")

		clientConn := createTestClient(lis)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)

		// act
		res, err := client.ExecuteActorStateTransaction(
			t.Context(),
			&runtimev1pb.ExecuteActorStateTransactionRequest{
				ActorId:   "fakeActorID",
				ActorType: "fakeActorType",
				Operations: []*runtimev1pb.TransactionalActorStateOperation{
					{
						OperationType: "upsert",
						Key:           "key1",
						Value:         &anypb.Any{Value: data},
						Metadata: map[string]string{
							"ttlInSeconds": "100",
						},
					},
					{
						OperationType: "delete",
						Key:           "key2",
					},
				},
			})

		// assert
		require.NoError(t, err)
		assert.NotNil(t, res)
	})
}

func TestInvokeActorRejectsInternalActorType(t *testing.T) {
	api := &api{
		Universal: universal.New(universal.Options{
			AppID:  "fakeAPI",
			Actors: actorsfake.New(),
		}),
	}
	lis := startDaprAPIServer(t, api, "")

	clientConn := createTestClient(lis)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	for _, actorType := range []string{
		"dapr.internal.default.fakeAPI.workflow",
		"dapr.internal.default.fakeAPI.activity",
		"dapr.internal.default.fakeAPI.executor",
		"dapr.internal.default.fakeAPI.retentioner",
	} {
		t.Run(actorType, func(t *testing.T) {
			_, err := client.InvokeActor(t.Context(), &runtimev1pb.InvokeActorRequest{
				ActorType: actorType,
				ActorId:   "fakeActorID",
				Method:    "Complete",
			})
			require.Error(t, err)
			assert.Equal(t, codes.PermissionDenied, status.Code(err))
			assert.Contains(t, err.Error(), "reserved for the Dapr workflow runtime")
		})
	}
}

func TestActorCallContextError(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		code codes.Code
	}{
		"deadline exceeded":         {err: context.DeadlineExceeded, code: codes.DeadlineExceeded},
		"wrapped deadline exceeded": {err: fmt.Errorf("failed to lookup actor: %w", context.DeadlineExceeded), code: codes.DeadlineExceeded},
		// daprd cancels calls internally (dissemination, shutdown); the caller
		// did not cancel, so that stays an internal error.
		"wrapped canceled": {err: fmt.Errorf("failed to lookup actor: %w", context.Canceled), code: codes.Internal},
		"other error":      {err: errors.New("boom"), code: codes.Internal},
	} {
		t.Run(name, func(t *testing.T) {
			api := &api{
				Universal: universal.New(universal.Options{
					AppID:      "fakeAPI",
					Resiliency: resiliency.New(nil),
					Actors: actorsfake.New().WithRouter(func(context.Context) (router.Interface, error) {
						return routerfake.New().WithCallFn(func(context.Context, *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
							return nil, tc.err
						}), nil
					}),
				}),
			}

			lis := startDaprAPIServer(t, api, "")
			conn := createTestClient(lis)
			defer conn.Close()
			_, err := runtimev1pb.NewDaprClient(conn).InvokeActor(t.Context(), &runtimev1pb.InvokeActorRequest{
				ActorType: "mytype", ActorId: "1", Method: "foo",
			})
			assert.Equal(t, tc.code, status.Code(err), "InvokeActor: %v", err)

			server, ilis := startInternalServer(api)
			defer server.Stop()
			iconn := createTestClient(ilis)
			defer iconn.Close()
			_, err = internalv1pb.NewServiceInvocationClient(iconn).CallActor(t.Context(), &internalv1pb.InternalInvokeRequest{
				Ver:     internalv1pb.APIVersion_V1,
				Actor:   &internalv1pb.Actor{ActorType: "mytype", ActorId: "1"},
				Message: &commonv1pb.InvokeRequest{Method: "foo"},
			})
			assert.Equal(t, tc.code, status.Code(err), "CallActor: %v", err)
		})
	}
}
