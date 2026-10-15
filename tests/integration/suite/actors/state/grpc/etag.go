/*
Copyright 2026 The Dapr Authors
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
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(etag))
}

type etag struct {
	app *actors.Actors
}

func (e *etag) Setup(t *testing.T) []framework.Option {
	e.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(e.app),
	}
}

func (e *etag) Run(t *testing.T, ctx context.Context) {
	e.app.WaitUntilRunning(t, ctx)
	client := e.app.GRPCClient(t, ctx)
	activation.Invoke(t, ctx, client, &rtv1.InvokeActorRequest{ActorType: "abc", ActorId: "1", Method: "foo"})

	get := func(key string) *rtv1.GetActorStateResponse {
		t.Helper()
		resp, err := client.GetActorState(ctx, &rtv1.GetActorStateRequest{ActorType: "abc", ActorId: "1", Key: key})
		require.NoError(t, err)
		return resp
	}
	upsert := func(key, value, etag string) *rtv1.TransactionalActorStateOperation {
		return &rtv1.TransactionalActorStateOperation{OperationType: "upsert", Key: key, Value: &anypb.Any{Value: []byte(value)}, Etag: etag}
	}
	del := func(key, etag string) *rtv1.TransactionalActorStateOperation {
		return &rtv1.TransactionalActorStateOperation{OperationType: "delete", Key: key, Etag: etag}
	}
	txn := func(ops ...*rtv1.TransactionalActorStateOperation) error {
		_, err := client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{ActorType: "abc", ActorId: "1", Operations: ops})
		return err
	}

	assert.Empty(t, get("k").GetEtag())

	require.NoError(t, txn(upsert("k", "v1", "")))
	e1 := get("k").GetEtag()
	require.NotEmpty(t, e1)

	require.NoError(t, txn(upsert("k", "v2", e1)))
	resp := get("k")
	e2 := resp.GetEtag()
	require.NotEmpty(t, e2)
	assert.NotEqual(t, e1, e2)
	assert.Equal(t, "v2", string(resp.GetData()))

	err := txn(upsert("other", "o", ""), upsert("k", "v3", e1))
	assert.Equal(t, codes.Aborted, status.Code(err), err)
	resp = get("k")
	assert.Equal(t, e2, resp.GetEtag())
	assert.Equal(t, "v2", string(resp.GetData()))
	assert.Empty(t, get("other").GetData())

	err = txn(del("k", e1))
	assert.Equal(t, codes.Aborted, status.Code(err), err)
	assert.Equal(t, "v2", string(get("k").GetData()))

	err = txn(upsert("missing", "x", e2))
	assert.Equal(t, codes.Aborted, status.Code(err), err)

	require.NoError(t, txn(upsert("k", "v4", "")))
	resp = get("k")
	assert.Equal(t, "v4", string(resp.GetData()))

	require.NoError(t, txn(del("k", resp.GetEtag())))
	assert.Empty(t, get("k").GetData())
	assert.Empty(t, get("k").GetEtag())
}
