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
	suite.Register(new(actorstatestorebinding))
}

// actorstatestorebinding ensures that hot reloading the state store bound to
// an actor type through spec.actors.stateStores affects only that actor type:
// removing the store makes its state API fail with
// ERR_ACTOR_STATE_STORE_NOT_FOUND, without falling back to the default actor
// state store and without touching actor hosting or other actor types, and
// adding the store back recovers the state that was already in it.
type actorstatestorebinding struct {
	app       *actors.Actors
	defaultDB *sqlite.SQLite
	txDB      *sqlite.SQLite

	// compFile is the hot-reloadable component file of the bound store.
	compFile string
	compYAML string

	boundStore string
	boundType  string
	plainType  string
}

func (a *actorstatestorebinding) Setup(t *testing.T) []framework.Option {
	a.boundStore = "wallet-transactions"
	a.boundType = "TransactionActor"
	a.plainType = "PlainActor"

	a.defaultDB = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	a.txDB = sqlite.New(t,
		sqlite.WithName(a.boundStore),
		sqlite.WithCreateStateTables(),
	)

	resDir := t.TempDir()
	a.compFile = filepath.Join(resDir, a.boundStore+".yaml")
	a.compYAML = a.txDB.GetComponent(t)
	require.NoError(t, os.WriteFile(a.compFile, []byte(a.compYAML), 0o600))

	noop := func(nethttp.ResponseWriter, *nethttp.Request) {}
	a.app = actors.New(t,
		actors.WithDB(a.defaultDB),
		actors.WithActorTypes(a.boundType, a.plainType),
		actors.WithActorTypeHandler(a.boundType, noop),
		actors.WithActorTypeHandler(a.plainType, noop),
		actors.WithDaprdOptions(
			daprd.WithResourcesDir(resDir),
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

func (a *actorstatestorebinding) Run(t *testing.T, ctx context.Context) {
	a.app.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	gclient := a.app.GRPCClient(t, ctx)
	baseURL := "http://" + a.app.Daprd().HTTPAddress() + "/v1.0/actors/"

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

	invoke := func(t require.TestingT, actorType string) {
		code, body := doHTTP(t, nethttp.MethodPost, actorType+"/a1/method/foo", "")
		require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)
		_, err := gclient.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: actorType, ActorId: "a1", Method: "foo",
		})
		require.NoError(t, err)
	}

	saveHTTP := func(t require.TestingT, actorType, key string) (int, string) {
		return doHTTP(t, nethttp.MethodPost, actorType+"/a1/state",
			`[{"operation":"upsert","request":{"key":"`+key+`","value":"v1"}}]`)
	}

	saveGRPC := func(actorType, key string) error {
		_, err := gclient.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
			ActorType: actorType,
			ActorId:   "a1",
			Operations: []*rtv1.TransactionalActorStateOperation{{
				OperationType: "upsert",
				Key:           key,
				Value:         &anypb.Any{Value: []byte("v1")},
			}},
		})
		return err
	}

	getGRPC := func(actorType, key string) (string, error) {
		res, err := gclient.GetActorState(ctx, &rtv1.GetActorStateRequest{
			ActorType: actorType, ActorId: "a1", Key: key,
		})
		return string(res.GetData()), err
	}

	appID := a.app.AppID()
	boundKeys := []string{
		appID + "||" + a.boundType + "||a1||kg",
		appID + "||" + a.boundType + "||a1||kh",
	}

	t.Run("bound store loaded", func(t *testing.T) {
		for _, actorType := range []string{a.boundType, a.plainType} {
			invoke(t, actorType)

			code, body := saveHTTP(t, actorType, "kh")
			require.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)
			require.NoError(t, saveGRPC(actorType, "kg"))

			code, body = doHTTP(t, nethttp.MethodGet, actorType+"/a1/state/kh", "")
			require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)
			assert.JSONEq(t, `"v1"`, body)
			got, err := getGRPC(actorType, "kg")
			require.NoError(t, err)
			assert.Equal(t, "v1", got)
		}

		assert.Equal(t, boundKeys, a.txDB.StateKeys(t, ctx))
	})

	t.Run("bound store removed", func(t *testing.T) {
		require.NoError(t, os.Remove(a.compFile))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := doHTTP(c, nethttp.MethodGet, a.boundType+"/a1/state/kh", "")
			assert.Equal(c, nethttp.StatusInternalServerError, code)
			assert.Contains(c, body, "ERR_ACTOR_STATE_STORE_NOT_FOUND")
			assert.Contains(c, body, a.boundStore)
		}, 20*time.Second, 10*time.Millisecond)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := getGRPC(a.boundType, "kg")
			s, ok := status.FromError(err)
			if assert.True(c, ok) {
				assert.Equal(c, codes.Internal, s.Code())
				assert.Contains(c, s.Message(), a.boundStore)
			}
		}, 20*time.Second, 10*time.Millisecond)

		// Writes to the bound type fail too, and never fall back to the
		// default actor state store.
		code, body := saveHTTP(t, a.boundType, "kx")
		assert.Equalf(t, nethttp.StatusInternalServerError, code, "body: %s", body)
		assert.Contains(t, body, "ERR_ACTOR_STATE_STORE_NOT_FOUND")
		err := saveGRPC(a.boundType, "ky")
		assert.Equal(t, codes.Internal, status.Code(err))

		// Hosting and the unbound type are unaffected.
		invoke(t, a.boundType)
		invoke(t, a.plainType)
		code, body = saveHTTP(t, a.plainType, "kh2")
		assert.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)
		require.NoError(t, saveGRPC(a.plainType, "kg2"))
		got, err := getGRPC(a.plainType, "kg2")
		require.NoError(t, err)
		assert.Equal(t, "v1", got)

		assert.Equal(t, []string{
			appID + "||" + a.plainType + "||a1||kg",
			appID + "||" + a.plainType + "||a1||kg2",
			appID + "||" + a.plainType + "||a1||kh",
			appID + "||" + a.plainType + "||a1||kh2",
		}, a.defaultDB.StateKeys(t, ctx))
	})

	t.Run("bound store added back", func(t *testing.T) {
		require.NoError(t, os.WriteFile(a.compFile, []byte(a.compYAML), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := doHTTP(c, nethttp.MethodGet, a.boundType+"/a1/state/kh", "")
			assert.Equal(c, nethttp.StatusOK, code)
			assert.JSONEq(c, `"v1"`, body)
		}, 20*time.Second, 10*time.Millisecond)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			got, err := getGRPC(a.boundType, "kg")
			if assert.NoError(c, err) {
				assert.Equal(c, "v1", got)
			}
		}, 20*time.Second, 10*time.Millisecond)

		assert.Equal(t, boundKeys, a.txDB.StateKeys(t, ctx))
	})
}
