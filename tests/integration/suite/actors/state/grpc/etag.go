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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	fwetag "github.com/dapr/dapr/tests/integration/framework/actors/etag"
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

	get := func(key string) (string, string) {
		t.Helper()
		data, tag, err := fwetag.GetGRPC(ctx, client, "abc", "1", key)
		require.NoError(t, err)
		return data, tag
	}
	txn := func(ops ...fwetag.Op) string {
		t.Helper()
		_, code, err := fwetag.SaveGRPC(ctx, client, "abc", "1", false, ops...)
		require.NoError(t, err)
		return code
	}
	mismatch := codes.Aborted.String()

	_, tag := get("k")
	assert.Empty(t, tag)

	require.Equal(t, "ok", txn(fwetag.Op{Key: "k", Value: "v1"}))
	_, e1 := get("k")
	require.NotEmpty(t, e1)

	require.Equal(t, "ok", txn(fwetag.Op{Key: "k", Value: "v2", ETag: e1}))
	data, e2 := get("k")
	require.NotEmpty(t, e2)
	assert.NotEqual(t, e1, e2)
	assert.Equal(t, "v2", data)

	assert.Equal(t, mismatch, txn(fwetag.Op{Key: "other", Value: "o"}, fwetag.Op{Key: "k", Value: "v3", ETag: e1}))
	data, tag = get("k")
	assert.Equal(t, e2, tag)
	assert.Equal(t, "v2", data)
	data, _ = get("other")
	assert.Empty(t, data)

	assert.Equal(t, mismatch, txn(fwetag.Op{Key: "k", ETag: e1, Delete: true}))
	data, _ = get("k")
	assert.Equal(t, "v2", data)

	assert.Equal(t, mismatch, txn(fwetag.Op{Key: "missing", Value: "x", ETag: e2}))

	require.Equal(t, "ok", txn(fwetag.Op{Key: "k", Value: "v4"}))
	data, e4 := get("k")
	assert.Equal(t, "v4", data)

	require.Equal(t, "ok", txn(fwetag.Op{Key: "k", ETag: e4, Delete: true}))
	data, tag = get("k")
	assert.Empty(t, data)
	assert.Empty(t, tag)
}
