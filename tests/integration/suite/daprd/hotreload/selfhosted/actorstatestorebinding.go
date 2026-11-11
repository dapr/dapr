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
	"sync/atomic"
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
// removing the store deactivates the actors of that type and makes its state
// API fail with ERR_ACTOR_STATE_STORE_NOT_FOUND, without falling back to the
// default actor state store and without touching actor hosting or other actor
// types. Adding the store back recovers the state that was already in it, and
// reloading it in place against another database deactivates the actors of
// that type again, so none keeps state from the previous database.
type actorstatestorebinding struct {
	app       *actors.Actors
	defaultDB *sqlite.SQLite
	txDB      *sqlite.SQLite
	// otherDB backs the bound store after it is reloaded in place.
	otherDB *sqlite.SQLite

	// deactivated counts the deactivation calls daprd makes to the app, per
	// actor type.
	boundDeactivated atomic.Int64
	plainDeactivated atomic.Int64

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
	a.otherDB = sqlite.New(t,
		sqlite.WithName(a.boundStore),
		sqlite.WithCreateStateTables(),
	)

	resDir := t.TempDir()
	a.compFile = filepath.Join(resDir, a.boundStore+".yaml")
	a.compYAML = a.txDB.GetComponent(t)
	require.NoError(t, os.WriteFile(a.compFile, []byte(a.compYAML), 0o600))

	counting := func(deactivated *atomic.Int64) nethttp.HandlerFunc {
		return func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				deactivated.Add(1)
			}
		}
	}
	a.app = actors.New(t,
		actors.WithDB(a.defaultDB),
		actors.WithActorTypes(a.boundType, a.plainType),
		actors.WithActorTypeHandler(a.boundType, counting(&a.boundDeactivated)),
		actors.WithActorTypeHandler(a.plainType, counting(&a.plainDeactivated)),
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
		framework.WithProcesses(a.txDB, a.otherDB, a.app),
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

		// The active actor of the bound type is deactivated, and only it.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, int64(1), a.boundDeactivated.Load())
		}, 20*time.Second, 10*time.Millisecond)
		assert.Equal(t, int64(0), a.plainDeactivated.Load())

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
		// default actor state store. The deactivated actor is activated
		// again first, as writes need an active actor.
		invoke(t, a.boundType)
		code, body := saveHTTP(t, a.boundType, "kx")
		assert.Equalf(t, nethttp.StatusInternalServerError, code, "body: %s", body)
		assert.Contains(t, body, "ERR_ACTOR_STATE_STORE_NOT_FOUND")
		err := saveGRPC(a.boundType, "ky")
		assert.Equal(t, codes.Internal, status.Code(err))

		// Hosting and the unbound type are unaffected.
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

		// The actor activated again while the store was missing is
		// deactivated, so it does not keep what it read without the store.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, int64(2), a.boundDeactivated.Load())
		}, 20*time.Second, 10*time.Millisecond)
		assert.Equal(t, int64(0), a.plainDeactivated.Load())

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

	t.Run("bound store reloaded in place against another database", func(t *testing.T) {
		// Activate the bound type's actor against the current database.
		invoke(t, a.boundType)
		require.Equal(t, int64(2), a.boundDeactivated.Load())

		require.NoError(t, os.WriteFile(a.compFile, []byte(a.otherDB.GetComponent(t)), 0o600))

		// The actor is deactivated, so none keeps state read from the
		// previous database.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, int64(3), a.boundDeactivated.Load())
		}, 20*time.Second, 10*time.Millisecond)
		assert.Equal(t, int64(0), a.plainDeactivated.Load())

		// State operations use the new database: the previous keys are not
		// there, and new writes land only in it.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			code, _ := doHTTP(c, nethttp.MethodGet, a.boundType+"/a1/state/kh", "")
			assert.Equal(c, nethttp.StatusNoContent, code)
		}, 20*time.Second, 10*time.Millisecond)
		got, err := getGRPC(a.boundType, "kg")
		require.NoError(t, err)
		assert.Empty(t, got)

		invoke(t, a.boundType)
		code, body := saveHTTP(t, a.boundType, "kh3")
		require.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)
		require.NoError(t, saveGRPC(a.boundType, "kg3"))

		assert.Equal(t, []string{
			appID + "||" + a.boundType + "||a1||kg3",
			appID + "||" + a.boundType + "||a1||kh3",
		}, a.otherDB.StateKeys(t, ctx))
		assert.Equal(t, boundKeys, a.txDB.StateKeys(t, ctx))
	})
}
