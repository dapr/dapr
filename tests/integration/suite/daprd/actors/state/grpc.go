/*
Copyright 2024 The Dapr Authors
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

package state

import (
	"context"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	app *actors.Actors
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {
		}),
	)

	return []framework.Option{
		framework.WithProcesses(g.app),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.app.WaitUntilRunning(t, ctx)

	client := g.app.GRPCClient(t, ctx)

	_, err := client.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: "abc",
		ActorId:   "123",
		Key:       "key1",
	})
	require.NoError(t, err)

	_, err = client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: "abc",
		ActorId:   "123",
		Operations: []*rtv1.TransactionalActorStateOperation{{
			OperationType: "upsert",
			Key:           "mykey",
			Value:         &anypb.Any{Value: []byte("myvalue")},
		}},
	})
	require.Error(t, err)
	s, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal.String(), s.Code().String())
	assert.Equal(t, "actor instance is missing", s.Message())

	_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "abc",
		ActorId:   "123",
		Method:    "method1",
	})
	require.NoError(t, err)

	res, err := client.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: "abc",
		ActorId:   "123",
		Key:       "key1",
	})
	require.NoError(t, err)
	assert.Empty(t, res.GetData())

	_, err = client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: "abc",
		ActorId:   "123",
		Operations: []*rtv1.TransactionalActorStateOperation{
			{
				OperationType: "upsert",
				Key:           "key1",
				Value:         &anypb.Any{Value: []byte("myvalue1")},
			},
			{
				OperationType: "upsert",
				Key:           "key2",
				Value:         &anypb.Any{Value: []byte("myvalue2")},
			},
		},
	})
	require.NoError(t, err)

	res, err = client.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: "abc",
		ActorId:   "123",
		Key:       "key1",
	})
	require.NoError(t, err)
	assert.Equal(t, "myvalue1", string(res.GetData()))
	res, err = client.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: "abc",
		ActorId:   "123",
		Key:       "key2",
	})
	require.NoError(t, err)
	assert.Equal(t, "myvalue2", string(res.GetData()))

	_, err = client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: "abc",
		ActorId:   "123",
		Operations: []*rtv1.TransactionalActorStateOperation{
			{
				OperationType: "delete",
				Key:           "key1",
			},
			{
				OperationType: "delete",
				Key:           "key2",
			},
		},
	})
	require.NoError(t, err)

	res, err = client.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: "abc",
		ActorId:   "123",
		Key:       "key1",
	})
	require.NoError(t, err)
	assert.Empty(t, res.GetData())
	res, err = client.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: "abc",
		ActorId:   "123",
		Key:       "key2",
	})
	require.NoError(t, err)
	assert.Empty(t, res.GetData())
}
