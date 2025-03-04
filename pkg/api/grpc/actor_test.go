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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/api/universal"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestGetActorState(t *testing.T) {
	t.Run("Get actor state - OK", func(t *testing.T) {
		data := []byte(`{ "data": 123 }`)

		actors := actorsfake.New()
		actors.WithState(func(context.Context) (state.Interface, error) {
			return statefake.New().WithGetFn(func(ctx context.Context, req *actorsapi.GetStateRequest) (*actorsapi.StateResponse, error) {
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
		res, err := client.GetActorState(context.Background(), &runtimev1pb.GetActorStateRequest{
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
			return statefake.New().WithTransactionalStateOperationFn(func(ctx context.Context, _ bool, req *actorsapi.TransactionalRequest) error {
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
			context.Background(),
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
