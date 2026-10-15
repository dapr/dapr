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

package state

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(etaghttp))
}

type etaghttp struct {
	app *actors.Actors
}

func (e *etaghttp) Setup(t *testing.T) []framework.Option {
	e.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(e.app),
	}
}

func (e *etaghttp) Run(t *testing.T, ctx context.Context) {
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

	txn := func(body string) (int, string) {
		t.Helper()
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, base, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		var apiErr struct {
			ErrorCode string `json:"errorCode"`
		}
		if len(b) > 0 {
			require.NoError(t, json.Unmarshal(b, &apiErr), string(b))
		}
		return resp.StatusCode, apiErr.ErrorCode
	}

	code, etag, _ := get("k")
	assert.Equal(t, nethttp.StatusNoContent, code)
	assert.Empty(t, etag)

	code, _ = txn(`[{"operation":"upsert","request":{"key":"k","value":"v1"}}]`)
	require.Equal(t, nethttp.StatusNoContent, code)
	code, e1, data := get("k")
	require.Equal(t, nethttp.StatusOK, code)
	require.NotEmpty(t, e1)
	assert.JSONEq(t, `"v1"`, data)

	code, _ = txn(`[{"operation":"upsert","request":{"key":"k","value":"v2","etag":"` + e1 + `"}}]`)
	require.Equal(t, nethttp.StatusNoContent, code)
	_, e2, data := get("k")
	require.NotEmpty(t, e2)
	assert.NotEqual(t, e1, e2)
	assert.JSONEq(t, `"v2"`, data)

	code, errCode := txn(`[` +
		`{"operation":"upsert","request":{"key":"other","value":"o"}},` +
		`{"operation":"upsert","request":{"key":"k","value":"v3","etag":"` + e1 + `"}}]`)
	assert.Equal(t, nethttp.StatusConflict, code)
	assert.Equal(t, "ERR_ACTOR_STATE_ETAG_MISMATCH", errCode)
	_, etag, data = get("k")
	assert.Equal(t, e2, etag)
	assert.JSONEq(t, `"v2"`, data)
	code, _, _ = get("other")
	assert.Equal(t, nethttp.StatusNoContent, code)

	code, errCode = txn(`[{"operation":"delete","request":{"key":"k","etag":"` + e1 + `"}}]`)
	assert.Equal(t, nethttp.StatusConflict, code)
	assert.Equal(t, "ERR_ACTOR_STATE_ETAG_MISMATCH", errCode)
	_, _, data = get("k")
	assert.JSONEq(t, `"v2"`, data)

	code, errCode = txn(`[{"operation":"upsert","request":{"key":"missing","value":"x","etag":"` + e2 + `"}}]`)
	assert.Equal(t, nethttp.StatusConflict, code)
	assert.Equal(t, "ERR_ACTOR_STATE_ETAG_MISMATCH", errCode)

	code, _ = txn(`[{"operation":"upsert","request":{"key":"k","value":"v4"}}]`)
	require.Equal(t, nethttp.StatusNoContent, code)
	_, e4, data := get("k")
	assert.JSONEq(t, `"v4"`, data)

	code, _ = txn(`[{"operation":"delete","request":{"key":"k","etag":"` + e4 + `"}}]`)
	require.Equal(t, nethttp.StatusNoContent, code)
	code, _, _ = get("k")
	assert.Equal(t, nethttp.StatusNoContent, code)
}
