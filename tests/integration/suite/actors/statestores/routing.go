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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(routing))
}

// routing ensures that spec.actors.stateStores routes the state of each
// bound actor type to its own state store, while unbound actor types keep
// using the default actor state store.
type routing struct {
	app       *actors.Actors
	defaultDB *sqlite.SQLite
	txDB      *sqlite.SQLite
	balDB     *sqlite.SQLite

	// txType and balType are bound to the txDB and balDB state stores;
	// plainType is unbound.
	txType    string
	balType   string
	plainType string
}

func (r *routing) Setup(t *testing.T) []framework.Option {
	r.txType = "TransactionActor"
	r.balType = "BalanceActor"
	r.plainType = "PlainActor"

	return r.setup(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: wallet-transactions
      actorTypes: ["`+r.txType+`"]
    - name: wallet-balance
      actorTypes: ["`+r.balType+`"]
`)
}

// setup starts the stores and the app, with daprd loading each of the given
// Configuration manifests as its own --config file.
func (r *routing) setup(t *testing.T, configs ...string) []framework.Option {
	r.defaultDB = sqlite.New(t,
		sqlite.WithName("default"),
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	r.txDB = sqlite.New(t,
		sqlite.WithName("wallet-transactions"),
		sqlite.WithCreateStateTables(),
	)
	r.balDB = sqlite.New(t,
		sqlite.WithName("wallet-balance"),
		sqlite.WithCreateStateTables(),
	)

	noop := func(nethttp.ResponseWriter, *nethttp.Request) {}
	r.app = actors.New(t,
		actors.WithDB(r.defaultDB),
		actors.WithResources(r.txDB.GetComponent(t), r.balDB.GetComponent(t)),
		actors.WithActorTypes(r.txType, r.balType, r.plainType),
		actors.WithActorTypeHandler(r.txType, noop),
		actors.WithActorTypeHandler(r.balType, noop),
		actors.WithActorTypeHandler(r.plainType, noop),
		actors.WithDaprdOptions(daprd.WithConfigManifests(t, configs...)),
	)

	return []framework.Option{
		framework.WithProcesses(r.txDB, r.balDB, r.app),
	}
}

func (r *routing) Run(t *testing.T, ctx context.Context) {
	r.app.WaitUntilRunning(t, ctx)

	actorTypes := []string{r.txType, r.balType, r.plainType}

	t.Run("http", func(t *testing.T) {
		for _, actorType := range actorTypes {
			r.saveAndReadHTTP(t, ctx, actorType, "h1")
		}
	})

	t.Run("grpc", func(t *testing.T) {
		for _, actorType := range actorTypes {
			r.saveAndReadGRPC(t, ctx, actorType, "g1")
		}
	})

	appID := r.app.AppID()
	assert.Equal(t, []string{
		appID + "||" + r.txType + "||g1||k1",
		appID + "||" + r.txType + "||h1||k1",
	}, r.txDB.StateKeys(t, ctx))
	assert.Equal(t, []string{
		appID + "||" + r.balType + "||g1||k1",
		appID + "||" + r.balType + "||h1||k1",
	}, r.balDB.StateKeys(t, ctx))
	assert.Equal(t, []string{
		appID + "||" + r.plainType + "||g1||k1",
		appID + "||" + r.plainType + "||h1||k1",
	}, r.defaultDB.StateKeys(t, ctx))
}

// saveAndReadHTTP activates the actor, then writes and reads back key k1
// through the HTTP API.
func (r *routing) saveAndReadHTTP(t *testing.T, ctx context.Context, actorType, actorID string) {
	t.Helper()

	httpClient := client.HTTP(t)
	baseURL := "http://" + r.app.Daprd().HTTPAddress() + "/v1.0/actors/" + actorType + "/" + actorID

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

	code, body := do(nethttp.MethodPost, "/method/foo", "")
	require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)

	code, body = do(nethttp.MethodPost, "/state",
		`[{"operation":"upsert","request":{"key":"k1","value":"v1"}}]`)
	require.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)

	code, body = do(nethttp.MethodGet, "/state/k1", "")
	require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)
	assert.JSONEq(t, `"v1"`, body)
}

// saveAndReadGRPC activates the actor, then writes and reads back key k1
// through the gRPC API.
func (r *routing) saveAndReadGRPC(t *testing.T, ctx context.Context, actorType, actorID string) {
	t.Helper()

	gclient := r.app.GRPCClient(t, ctx)

	_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: actorType,
		ActorId:   actorID,
		Method:    "foo",
	})
	require.NoError(t, err)

	_, err = gclient.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: actorType,
		ActorId:   actorID,
		Operations: []*rtv1.TransactionalActorStateOperation{{
			OperationType: "upsert",
			Key:           "k1",
			Value:         &anypb.Any{Value: []byte("v1")},
		}},
	})
	require.NoError(t, err)

	res, err := gclient.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: actorType,
		ActorId:   actorID,
		Key:       "k1",
	})
	require.NoError(t, err)
	assert.Equal(t, "v1", string(res.GetData()))
}
