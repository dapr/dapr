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

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

var testActorResiliency = &v1alpha1.Resiliency{
	Spec: v1alpha1.ResiliencySpec{
		Policies: v1alpha1.Policies{
			Retries: map[string]v1alpha1.Retry{
				"singleRetry": {
					MaxRetries:  1,
					MaxInterval: "100ms",
					Policy:      "constant",
					Duration:    "10ms",
				},
			},
		},
		Targets: v1alpha1.Targets{
			Actors: map[string]v1alpha1.ActorPolicyNames{
				"failingActorType": {
					Retry: "singleRetry",
				},
			},
		},
	},
}

func TestRegisterActorReminder(t *testing.T) {
	t.Run("actors not initialized", func(t *testing.T) {
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, &api{
			id: "fakeAPI",
		}, "")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		_, err := client.RegisterActorReminder(context.TODO(), &runtimev1pb.RegisterActorReminderRequest{})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestUnregisterActorTimer(t *testing.T) {
	t.Run("actors not initialized", func(t *testing.T) {
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, &api{
			id: "fakeAPI",
		}, "")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		_, err := client.UnregisterActorTimer(context.TODO(), &runtimev1pb.UnregisterActorTimerRequest{})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestRegisterActorTimer(t *testing.T) {
	t.Run("actors not initialized", func(t *testing.T) {
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, &api{
			id: "fakeAPI",
		}, "")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		_, err := client.RegisterActorTimer(context.TODO(), &runtimev1pb.RegisterActorTimerRequest{})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestGetActorState(t *testing.T) {
	t.Run("actors not initialized", func(t *testing.T) {
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, &api{
			id: "fakeAPI",
		}, "")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		_, err := client.GetActorState(context.TODO(), &runtimev1pb.GetActorStateRequest{})
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("Get actor state - OK", func(t *testing.T) {
		data := []byte("{ \"data\": 123 }")
		mockActors := new(actors.MockActors)
		mockActors.On("GetState", &actors.GetStateRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		}).Return(&actors.StateResponse{
			Data: data,
		}, nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, &api{
			id:    "fakeAPI",
			actor: mockActors,
		}, "")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)

		// act
		res, err := client.GetActorState(context.TODO(), &runtimev1pb.GetActorStateRequest{
			ActorId:   "fakeActorID",
			ActorType: "fakeActorType",
			Key:       "key1",
		})

		// assert
		assert.Nil(t, err)
		assert.Equal(t, data, res.Data)
		mockActors.AssertNumberOfCalls(t, "GetState", 1)
	})
}

func TestExecuteActorStateTransaction(t *testing.T) {
	port, _ := freeport.GetFreePort()

	t.Run("actors not initialized", func(t *testing.T) {
		server := startDaprAPIServer(port, &api{
			id: "fakeAPI",
		}, "")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		_, err := client.ExecuteActorStateTransaction(context.TODO(), &runtimev1pb.ExecuteActorStateTransactionRequest{})
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("Save actor state - Upsert and Delete OK", func(t *testing.T) {
		data := []byte("{ \"data\": 123 }")
		mockActors := new(actors.MockActors)
		mockActors.On("TransactionalStateOperation", &actors.TransactionalRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
			Operations: []actors.TransactionalOperation{
				{
					Operation: "upsert",
					Request: map[string]interface{}{
						"key":   "key1",
						"value": data,
					},
				},
				{
					Operation: "delete",
					Request: map[string]interface{}{
						"key": "key2",
					},
				},
			},
		}).Return(nil)

		mockActors.On("IsActorHosted", &actors.ActorHostedRequest{
			ActorID:   "fakeActorID",
			ActorType: "fakeActorType",
		}).Return(true)

		server := startDaprAPIServer(port, &api{
			id:    "fakeAPI",
			actor: mockActors,
		}, "")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)

		// act
		res, err := client.ExecuteActorStateTransaction(context.TODO(),
			&runtimev1pb.ExecuteActorStateTransactionRequest{
				ActorId:   "fakeActorID",
				ActorType: "fakeActorType",
				Operations: []*runtimev1pb.TransactionalActorStateOperation{
					{
						OperationType: "upsert",
						Key:           "key1",
						Value:         &anypb.Any{Value: data},
					},
					{
						OperationType: "delete",
						Key:           "key2",
					},
				},
			})

		// assert
		assert.Nil(t, err)
		assert.NotNil(t, res)
		mockActors.AssertNumberOfCalls(t, "TransactionalStateOperation", 1)
	})
}

func TestUnregisterActorReminder(t *testing.T) {
	t.Run("actors not initialized", func(t *testing.T) {
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, &api{
			id: "fakeAPI",
		}, "")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		_, err := client.UnregisterActorReminder(context.TODO(), &runtimev1pb.UnregisterActorReminderRequest{})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestInvokeActor(t *testing.T) {
	t.Run("actors not initialized", func(t *testing.T) {
		port, _ := freeport.GetFreePort()
		server := startDaprAPIServer(port, &api{
			id: "fakeAPI",
		}, "")
		defer server.Stop()

		clientConn := createTestClient(port)
		defer clientConn.Close()

		client := runtimev1pb.NewDaprClient(clientConn)
		_, err := client.InvokeActor(context.TODO(), &runtimev1pb.InvokeActorRequest{})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestInvokeActorWithResiliency(t *testing.T) {
	failingActors := actors.FailingActors{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingActor": 1,
			},
			nil,
			map[string]int{},
		),
	}

	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, &api{
		id:         "fakeAPI",
		actor:      &failingActors,
		resiliency: resiliency.FromConfigurations(logger.NewLogger("grpc.api.test"), testActorResiliency),
	}, "")
	defer server.Stop()

	t.Run("actors recover from error with resiliency", func(t *testing.T) {
		clientConn := createTestClient(port)
		defer clientConn.Close()

		req := &runtimev1pb.InvokeActorRequest{}
		req.ActorType = "failingActorType"
		req.ActorId = "failingActor"

		client := runtimev1pb.NewDaprClient(clientConn)
		_, err := client.InvokeActor(context.Background(), req)
		assert.NoError(t, err)
		assert.Equal(t, codes.OK, status.Code(err))
		assert.Equal(t, 2, failingActors.Failure.CallCount("failingActor"))
	})
}
