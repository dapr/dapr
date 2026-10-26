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

package http

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	fwetag "github.com/dapr/dapr/tests/integration/framework/actors/etag"
	"github.com/dapr/dapr/tests/integration/framework/client"
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
	activation.Invoke(t, ctx, e.app.GRPCClient(t, ctx), &rtv1.InvokeActorRequest{ActorType: "abc", ActorId: "1", Method: "foo"})

	httpClient := client.HTTP(t)
	base := fmt.Sprintf("http://%s/v1.0/actors/abc/1/state", e.app.Daprd().HTTPAddress())

	get := func(key string) (int, string, string) {
		t.Helper()
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, base+"/"+key, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, resp.Header.Get("ETag"), string(b)
	}

	txn := func(ops ...fwetag.Op) string {
		t.Helper()
		_, code, err := fwetag.SaveHTTP(ctx, httpClient, e.app.Daprd().HTTPAddress(), "abc", "1", false, ops...)
		require.NoError(t, err)
		return code
	}

	code, etag, _ := get("k")
	assert.Equal(t, nethttp.StatusNoContent, code)
	assert.Empty(t, etag)

	require.Equal(t, "ok", txn(fwetag.Op{Key: "k", Value: "v1"}))
	code, e1, data := get("k")
	require.Equal(t, nethttp.StatusOK, code)
	require.NotEmpty(t, e1)
	assert.JSONEq(t, `"v1"`, data)

	require.Equal(t, "ok", txn(fwetag.Op{Key: "k", Value: "v2", ETag: e1}))
	_, e2, data := get("k")
	require.NotEmpty(t, e2)
	assert.NotEqual(t, e1, e2)
	assert.JSONEq(t, `"v2"`, data)

	mismatch := "409 ERR_ACTOR_STATE_ETAG_MISMATCH"
	assert.Equal(t, mismatch, txn(fwetag.Op{Key: "other", Value: "o"}, fwetag.Op{Key: "k", Value: "v3", ETag: e1}))
	_, etag, data = get("k")
	assert.Equal(t, e2, etag)
	assert.JSONEq(t, `"v2"`, data)
	code, _, _ = get("other")
	assert.Equal(t, nethttp.StatusNoContent, code)

	assert.Equal(t, mismatch, txn(fwetag.Op{Key: "k", ETag: e1, Delete: true}))
	_, _, data = get("k")
	assert.JSONEq(t, `"v2"`, data)

	assert.Equal(t, mismatch, txn(fwetag.Op{Key: "missing", Value: "x", ETag: e2}))

	require.Equal(t, "ok", txn(fwetag.Op{Key: "k", Value: "v4"}))
	_, e4, data := get("k")
	assert.JSONEq(t, `"v4"`, data)

	require.Equal(t, "ok", txn(fwetag.Op{Key: "k", ETag: e4, Delete: true}))
	code, _, _ = get("k")
	assert.Equal(t, nethttp.StatusNoContent, code)
}
