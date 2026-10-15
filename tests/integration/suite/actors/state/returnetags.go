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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(&returnetagsHTTP{returnetags{viaGRPC: false}})
	suite.Register(&returnetagsGRPC{returnetags{viaGRPC: true}})
}

type (
	returnetagsHTTP struct{ returnetags }
	returnetagsGRPC struct{ returnetags }
)

type returnetags struct {
	viaGRPC bool
	app     *actors.Actors
}

type etagOp struct {
	del        bool
	key        string
	value      string
	etag       string
	createOnly bool
}

type etagResult struct {
	etags map[string]string
	// "ok", the gRPC code, or the HTTP status and error code.
	code string
}

func (r *returnetags) Setup(t *testing.T) []framework.Option {
	r.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithActorTypeHandler("abc", func(nethttp.ResponseWriter, *nethttp.Request) {}),
	)

	return []framework.Option{
		framework.WithProcesses(r.app),
	}
}

func (r *returnetags) Run(t *testing.T, ctx context.Context) {
	r.app.WaitUntilRunning(t, ctx)
	grpcCl := r.app.GRPCClient(t, ctx)
	activation.Invoke(t, ctx, grpcCl, &rtv1.InvokeActorRequest{ActorType: "abc", ActorId: "1", Method: "foo"})
	httpCl := client.HTTP(t)
	base := fmt.Sprintf("http://%s/v1.0/actors/abc/1/state", r.app.Daprd().HTTPAddress())

	get := func(key string) (string, string) {
		t.Helper()
		resp, err := grpcCl.GetActorState(ctx, &rtv1.GetActorStateRequest{ActorType: "abc", ActorId: "1", Key: key})
		require.NoError(t, err)
		return strings.Trim(string(resp.GetData()), `"`), resp.GetEtag()
	}

	txnGRPC := func(returnETags bool, ops ...etagOp) etagResult {
		t.Helper()
		in := &rtv1.ExecuteActorStateTransactionRequest{ActorType: "abc", ActorId: "1", ReturnEtags: returnETags}
		for _, o := range ops {
			op := &rtv1.TransactionalActorStateOperation{OperationType: "upsert", Key: o.key, Etag: o.etag, CreateOnly: o.createOnly}
			if o.del {
				op.OperationType = "delete"
			} else {
				op.Value = &anypb.Any{Value: []byte(`"` + o.value + `"`)}
			}
			in.Operations = append(in.Operations, op)
		}
		resp, err := grpcCl.ExecuteActorStateTransaction(ctx, in)
		if err != nil {
			return etagResult{code: status.Code(err).String()}
		}
		if !returnETags {
			assert.Zero(t, proto.Size(resp))
		}
		return etagResult{etags: resp.GetEtags(), code: "ok"}
	}

	txnHTTP := func(returnETags bool, ops ...etagOp) etagResult {
		t.Helper()
		reqs := make([]map[string]any, 0, len(ops))
		for _, o := range ops {
			req := map[string]any{"key": o.key}
			if o.etag != "" {
				req["etag"] = o.etag
			}
			if o.createOnly {
				req["createOnly"] = true
			}
			op := "delete"
			if !o.del {
				op = "upsert"
				req["value"] = o.value
			}
			reqs = append(reqs, map[string]any{"operation": op, "request": req})
		}
		body, err := json.Marshal(reqs)
		require.NoError(t, err)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, base, strings.NewReader(string(body)))
		require.NoError(t, err)
		if returnETags {
			req.Header.Set("dapr-return-etags", "true")
		}
		resp, err := httpCl.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		switch {
		case !returnETags && resp.StatusCode == nethttp.StatusNoContent:
			assert.Empty(t, b)
			return etagResult{code: "ok"}
		case returnETags && resp.StatusCode == nethttp.StatusOK:
			assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
			var res struct {
				ETags map[string]string `json:"etags"`
			}
			require.NoError(t, json.Unmarshal(b, &res), string(b))
			require.NotNil(t, res.ETags, string(b))
			return etagResult{etags: res.ETags, code: "ok"}
		}
		var apiErr struct {
			ErrorCode string `json:"errorCode"`
		}
		require.NoError(t, json.Unmarshal(b, &apiErr), string(b))
		return etagResult{code: fmt.Sprintf("%d %s", resp.StatusCode, apiErr.ErrorCode)}
	}

	txn := txnHTTP
	mismatch, invalid, badRequest := "409 ERR_ACTOR_STATE_ETAG_MISMATCH", "400 ERR_ACTOR_STATE_ETAG_INVALID", "400 ERR_BAD_REQUEST"
	if r.viaGRPC {
		txn = txnGRPC
		mismatch, invalid, badRequest = codes.Aborted.String(), codes.InvalidArgument.String(), codes.InvalidArgument.String()
	}

	t.Run("no opt-in keeps the old response", func(t *testing.T) {
		res := txn(false, etagOp{key: "plain", value: "p"})
		assert.Equal(t, "ok", res.code)
		assert.Empty(t, res.etags)
	})

	t.Run("returned etags equal a read", func(t *testing.T) {
		res := txn(true,
			etagOp{key: "a", value: "1"},
			etagOp{key: "b", value: "2", createOnly: true},
			etagOp{key: "plain", del: true},
		)
		require.Equal(t, "ok", res.code)
		require.Len(t, res.etags, 2)
		for k, v := range map[string]string{"a": "1", "b": "2"} {
			data, etag := get(k)
			assert.Equal(t, v, data)
			assert.Equal(t, etag, res.etags[k], k)
		}
		assert.NotContains(t, res.etags, "plain")
	})

	t.Run("returned etag guards the next save without a re-read", func(t *testing.T) {
		res := txn(true, etagOp{key: "c", value: "1", createOnly: true})
		require.Equal(t, "ok", res.code)
		stale := res.etags["c"]
		require.NotEmpty(t, stale)

		res = txn(true, etagOp{key: "c", value: "2", etag: stale})
		require.Equal(t, "ok", res.code)
		fresh := res.etags["c"]
		require.NotEmpty(t, fresh)
		assert.NotEqual(t, stale, fresh)

		res = txn(true, etagOp{key: "other", value: "o"}, etagOp{key: "c", value: "stale", etag: stale})
		assert.Equal(t, mismatch, res.code)
		assert.Nil(t, res.etags)
		data, etag := get("c")
		assert.Equal(t, "2", data)
		assert.Equal(t, fresh, etag)
		data, _ = get("other")
		assert.Empty(t, data)

		res = txn(true, etagOp{key: "c", del: true, etag: fresh})
		require.Equal(t, "ok", res.code)
		assert.Empty(t, res.etags)
		data, etag = get("c")
		assert.Empty(t, data)
		assert.Empty(t, etag)
	})

	t.Run("create-only on an existing key fails the whole transaction", func(t *testing.T) {
		require.Equal(t, "ok", txn(false, etagOp{key: "d", value: "newer"}).code)

		res := txn(true, etagOp{key: "other", value: "o"}, etagOp{key: "d", value: "stale", createOnly: true})
		assert.Equal(t, mismatch, res.code)
		data, _ := get("d")
		assert.Equal(t, "newer", data)
		data, _ = get("other")
		assert.Empty(t, data)

		assert.Equal(t, mismatch, txn(false, etagOp{key: "d", value: "stale", createOnly: true}).code)
	})

	t.Run("create-only with an etag or on a delete is invalid", func(t *testing.T) {
		_, etag := get("d")
		require.NotEmpty(t, etag)
		assert.Equal(t, invalid, txn(true, etagOp{key: "d", value: "x", etag: etag, createOnly: true}).code)
		assert.Equal(t, badRequest, txn(true, etagOp{key: "d", del: true, createOnly: true}).code)
		data, _ := get("d")
		assert.Equal(t, "newer", data)
	})
}
