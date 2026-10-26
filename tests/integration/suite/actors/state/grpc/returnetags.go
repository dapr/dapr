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
	suite.Register(new(returnetags))
}

type returnetags struct {
	app *actors.Actors
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
	cl := r.app.GRPCClient(t, ctx)
	activation.Invoke(t, ctx, cl, &rtv1.InvokeActorRequest{ActorType: "abc", ActorId: "1", Method: "foo"})

	get := func(key string) (string, string) {
		t.Helper()
		data, tag, err := fwetag.GetGRPC(ctx, cl, "abc", "1", key)
		require.NoError(t, err)
		return data, tag
	}

	txn := func(returnETags bool, ops ...fwetag.Op) (map[string]string, string) {
		t.Helper()
		etags, code, err := fwetag.SaveGRPC(ctx, cl, "abc", "1", returnETags, ops...)
		require.NoError(t, err)
		return etags, code
	}

	mismatch, invalid, badRequest := codes.Aborted.String(), codes.InvalidArgument.String(), codes.InvalidArgument.String()

	t.Run("no opt-in keeps the old response", func(t *testing.T) {
		etags, code := txn(false, fwetag.Op{Key: "plain", Value: "p"})
		assert.Equal(t, "ok", code)
		assert.Empty(t, etags)
	})

	t.Run("returned etags equal a read", func(t *testing.T) {
		etags, code := txn(true,
			fwetag.Op{Key: "a", Value: "1"},
			fwetag.Op{Key: "b", Value: "2", CreateOnly: true},
			fwetag.Op{Key: "plain", Delete: true},
		)
		require.Equal(t, "ok", code)
		require.Len(t, etags, 2)
		for k, v := range map[string]string{"a": "1", "b": "2"} {
			data, tag := get(k)
			assert.Equal(t, v, data)
			assert.Equal(t, tag, etags[k], k)
		}
		assert.NotContains(t, etags, "plain")
	})

	t.Run("returned etag guards the next save without a re-read", func(t *testing.T) {
		etags, code := txn(true, fwetag.Op{Key: "c", Value: "1", CreateOnly: true})
		require.Equal(t, "ok", code)
		stale := etags["c"]
		require.NotEmpty(t, stale)

		etags, code = txn(true, fwetag.Op{Key: "c", Value: "2", ETag: stale})
		require.Equal(t, "ok", code)
		fresh := etags["c"]
		require.NotEmpty(t, fresh)
		assert.NotEqual(t, stale, fresh)

		etags, code = txn(true, fwetag.Op{Key: "other", Value: "o"}, fwetag.Op{Key: "c", Value: "stale", ETag: stale})
		assert.Equal(t, mismatch, code)
		assert.Nil(t, etags)
		data, tag := get("c")
		assert.Equal(t, "2", data)
		assert.Equal(t, fresh, tag)
		data, _ = get("other")
		assert.Empty(t, data)

		etags, code = txn(true, fwetag.Op{Key: "c", Delete: true, ETag: fresh})
		require.Equal(t, "ok", code)
		assert.Empty(t, etags)
		data, tag = get("c")
		assert.Empty(t, data)
		assert.Empty(t, tag)
	})

	t.Run("create-only on an existing key fails the whole transaction", func(t *testing.T) {
		_, code := txn(false, fwetag.Op{Key: "d", Value: "newer"})
		require.Equal(t, "ok", code)

		_, code = txn(true, fwetag.Op{Key: "other", Value: "o"}, fwetag.Op{Key: "d", Value: "stale", CreateOnly: true})
		assert.Equal(t, mismatch, code)
		data, _ := get("d")
		assert.Equal(t, "newer", data)
		data, _ = get("other")
		assert.Empty(t, data)

		_, code = txn(false, fwetag.Op{Key: "d", Value: "stale", CreateOnly: true})
		assert.Equal(t, mismatch, code)
	})

	t.Run("create-only with an etag or on a delete is invalid", func(t *testing.T) {
		_, tag := get("d")
		require.NotEmpty(t, tag)
		_, code := txn(true, fwetag.Op{Key: "d", Value: "x", ETag: tag, CreateOnly: true})
		assert.Equal(t, invalid, code)
		_, code = txn(true, fwetag.Op{Key: "d", Delete: true, CreateOnly: true})
		assert.Equal(t, badRequest, code)
		data, _ := get("d")
		assert.Equal(t, "newer", data)
	})
}
