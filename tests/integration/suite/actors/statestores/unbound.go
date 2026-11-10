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

package statestores

import (
	"context"
	"io"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unbound))
}

// unbound ensures that, without spec.actors.stateStores, every actor type
// keeps using the default actor state store, even when other state stores
// are loaded next to it.
type unbound struct {
	app       *actors.Actors
	defaultDB *sqlite.SQLite
	txDB      *sqlite.SQLite
	balDB     *sqlite.SQLite
}

func (u *unbound) Setup(t *testing.T) []framework.Option {
	u.defaultDB = sqlite.New(t,
		sqlite.WithName("default"),
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	u.txDB = sqlite.New(t,
		sqlite.WithName("txstore"),
		sqlite.WithCreateStateTables(),
	)
	u.balDB = sqlite.New(t,
		sqlite.WithName("balstore"),
		sqlite.WithCreateStateTables(),
	)

	noop := func(nethttp.ResponseWriter, *nethttp.Request) {}
	u.app = actors.New(t,
		actors.WithDB(u.defaultDB),
		actors.WithResources(u.txDB.GetComponent(t), u.balDB.GetComponent(t)),
		actors.WithActorTypes("TransactionActor", "PlainActor"),
		actors.WithActorTypeHandler("TransactionActor", noop),
		actors.WithActorTypeHandler("PlainActor", noop),
	)

	return []framework.Option{
		framework.WithProcesses(u.txDB, u.balDB, u.app),
	}
}

func (u *unbound) Run(t *testing.T, ctx context.Context) {
	u.app.WaitUntilRunning(t, ctx)

	actorTypes := []string{"TransactionActor", "PlainActor"}

	t.Run("http", func(t *testing.T) {
		httpClient := client.HTTP(t)
		baseURL := "http://" + u.app.Daprd().HTTPAddress() + "/v1.0/actors/"

		do := func(method, path, body string) (int, string) {
			t.Helper()
			req, err := nethttp.NewRequestWithContext(ctx, method, baseURL+path, strings.NewReader(body))
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			return resp.StatusCode, string(b)
		}

		for _, actorType := range actorTypes {
			code, body := do(nethttp.MethodPost, actorType+"/h1/method/foo", "")
			require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)

			code, body = do(nethttp.MethodPost, actorType+"/h1/state",
				`[{"operation":"upsert","request":{"key":"k1","value":"v1"}}]`)
			require.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)

			code, body = do(nethttp.MethodGet, actorType+"/h1/state/k1", "")
			require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)
			assert.JSONEq(t, `"v1"`, body)
		}
	})

	t.Run("grpc", func(t *testing.T) {
		gclient := u.app.GRPCClient(t, ctx)

		for _, actorType := range actorTypes {
			_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: actorType,
				ActorId:   "g1",
				Method:    "foo",
			})
			require.NoError(t, err)

			_, err = gclient.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
				ActorType: actorType,
				ActorId:   "g1",
				Operations: []*rtv1.TransactionalActorStateOperation{{
					OperationType: "upsert",
					Key:           "k1",
					Value:         &anypb.Any{Value: []byte("v1")},
				}},
			})
			require.NoError(t, err)

			res, err := gclient.GetActorState(ctx, &rtv1.GetActorStateRequest{
				ActorType: actorType,
				ActorId:   "g1",
				Key:       "k1",
			})
			require.NoError(t, err)
			assert.Equal(t, "v1", string(res.GetData()))
		}
	})

	appID := u.app.AppID()
	assert.Equal(t, []string{
		appID + "||PlainActor||g1||k1",
		appID + "||PlainActor||h1||k1",
		appID + "||TransactionActor||g1||k1",
		appID + "||TransactionActor||h1||k1",
	}, u.defaultDB.StateKeys(t, ctx))
	assert.Empty(t, u.txDB.StateKeys(t, ctx))
	assert.Empty(t, u.balDB.StateKeys(t, ctx))
}
