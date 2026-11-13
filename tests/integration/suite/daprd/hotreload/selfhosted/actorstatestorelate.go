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

package selfhosted

import (
	"context"
	"io"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	suite.Register(new(actorstatestorelate))
}

// actorstatestorelate ensures that when the state store bound to an actor
// type is missing at startup, daprd still starts, the actor type's state
// operations fail with ERR_ACTOR_STATE_STORE_NOT_FOUND, and they start
// working once hot reload loads the store, without a restart.
type actorstatestorelate struct {
	app  *actors.Actors
	txDB *sqlite.SQLite

	resDir     string
	boundStore string
	boundType  string
}

func (a *actorstatestorelate) Setup(t *testing.T) []framework.Option {
	a.boundStore = "wallet-transactions"
	a.boundType = "TransactionActor"

	a.txDB = sqlite.New(t,
		sqlite.WithName(a.boundStore),
		sqlite.WithCreateStateTables(),
	)

	// The resources directory starts without the bound store.
	a.resDir = t.TempDir()

	a.app = actors.New(t,
		actors.WithActorTypes(a.boundType),
		actors.WithActorTypeHandler(a.boundType, func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithDaprdOptions(
			daprd.WithResourcesDir(a.resDir),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: `+a.boundStore+`
      actorTypes: ["`+a.boundType+`"]
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.txDB, a.app),
	}
}

func (a *actorstatestorelate) Run(t *testing.T, ctx context.Context) {
	a.app.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	gclient := a.app.GRPCClient(t, ctx)
	baseURL := "http://" + a.app.Daprd().HTTPAddress() + "/v1.0/actors/" + a.boundType + "/a1"

	doHTTP := func(t require.TestingT, method, path, body string) (int, string) {
		req, err := nethttp.NewRequestWithContext(ctx, method, baseURL+path, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, string(b)
	}

	saveGRPC := func(key string) error {
		_, err := gclient.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
			ActorType: a.boundType,
			ActorId:   "a1",
			Operations: []*rtv1.TransactionalActorStateOperation{{
				OperationType: "upsert",
				Key:           key,
				Value:         &anypb.Any{Value: []byte("v1")},
			}},
		})
		return err
	}

	code, body := doHTTP(t, nethttp.MethodPost, "/method/foo", "")
	require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)

	t.Run("bound store missing", func(t *testing.T) {
		code, body := doHTTP(t, nethttp.MethodPost, "/state",
			`[{"operation":"upsert","request":{"key":"kh","value":"v1"}}]`)
		assert.Equal(t, nethttp.StatusInternalServerError, code)
		assert.Contains(t, body, "ERR_ACTOR_STATE_STORE_NOT_FOUND")

		err := saveGRPC("kg")
		require.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Contains(t, err.Error(), a.boundStore)
	})

	t.Run("bound store loaded by hot reload", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(a.resDir, a.boundStore+".yaml"), []byte(a.txDB.GetComponent(t)), 0o600))

		// Hot reload loads the store asynchronously. Adding a store that was
		// missing deactivates nothing, as no state call succeeded without it,
		// so the actor stays active; invoking it first keeps each attempt
		// independent of that.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := doHTTP(c, nethttp.MethodPost, "/method/foo", "")
			if !assert.Equalf(c, nethttp.StatusOK, code, "body: %s", body) {
				return
			}
			code, body = doHTTP(c, nethttp.MethodPost, "/state",
				`[{"operation":"upsert","request":{"key":"kh","value":"v1"}}]`)
			assert.Equalf(c, nethttp.StatusNoContent, code, "body: %s", body)
		}, 20*time.Second, 10*time.Millisecond)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: a.boundType, ActorId: "a1", Method: "foo",
			})
			if !assert.NoError(c, err) {
				return
			}
			assert.NoError(c, saveGRPC("kg"))
		}, 20*time.Second, 10*time.Millisecond)

		code, body := doHTTP(t, nethttp.MethodGet, "/state/kh", "")
		require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)
		assert.JSONEq(t, `"v1"`, body)
		res, err := gclient.GetActorState(ctx, &rtv1.GetActorStateRequest{
			ActorType: a.boundType, ActorId: "a1", Key: "kg",
		})
		require.NoError(t, err)
		assert.Equal(t, "v1", string(res.GetData()))

		appID := a.app.AppID()
		assert.Equal(t, []string{
			appID + "||" + a.boundType + "||a1||kg",
			appID + "||" + a.boundType + "||a1||kh",
		}, a.txDB.StateKeys(t, ctx))
		assert.Empty(t, a.app.DB().StateKeys(t, ctx))
	})
}
