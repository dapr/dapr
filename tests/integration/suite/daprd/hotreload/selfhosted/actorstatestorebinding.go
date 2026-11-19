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
	nethttp "net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
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

	appID := a.app.AppID()
	boundKeys := []string{
		appID + "||" + a.boundType + "||a1||kg",
		appID + "||" + a.boundType + "||a1||kh",
	}

	t.Run("bound store loaded", func(t *testing.T) {
		for _, actorType := range []string{a.boundType, a.plainType} {
			a.app.InvokeActor(t, ctx, actorType, "a1")

			code, body := a.app.SaveActorStateHTTP(t, ctx, actorType, "a1", "kh", "v1")
			require.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)
			require.NoError(t, a.app.SaveActorStateGRPC(t, ctx, actorType, "a1", "kg", "v1"))

			code, body = a.app.ActorHTTP(t, ctx, nethttp.MethodGet, actorType, "a1", "/state/kh", "")
			require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)
			assert.JSONEq(t, `"v1"`, body)
			got, err := a.app.GetActorStateGRPC(t, ctx, actorType, "a1", "kg")
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
			code, body := a.app.ActorHTTP(c, ctx, nethttp.MethodGet, a.boundType, "a1", "/state/kh", "")
			assert.Equal(c, nethttp.StatusInternalServerError, code)
			assert.Contains(c, body, "ERR_ACTOR_STATE_STORE_NOT_FOUND")
			assert.Contains(c, body, a.boundStore)
		}, 20*time.Second, 10*time.Millisecond)

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := a.app.GetActorStateGRPC(t, ctx, a.boundType, "a1", "kg")
			s, ok := status.FromError(err)
			if assert.True(c, ok) {
				assert.Equal(c, codes.Internal, s.Code())
				assert.Contains(c, s.Message(), a.boundStore)
			}
		}, 20*time.Second, 10*time.Millisecond)

		// Writes to the bound type fail too, and never fall back to the
		// default actor state store. The deactivated actor is activated
		// again first, as writes need an active actor.
		a.app.InvokeActor(t, ctx, a.boundType, "a1")
		code, body := a.app.SaveActorStateHTTP(t, ctx, a.boundType, "a1", "kx", "v1")
		assert.Equalf(t, nethttp.StatusInternalServerError, code, "body: %s", body)
		assert.Contains(t, body, "ERR_ACTOR_STATE_STORE_NOT_FOUND")
		err := a.app.SaveActorStateGRPC(t, ctx, a.boundType, "a1", "ky", "v1")
		assert.Equal(t, codes.Internal, status.Code(err))

		// Hosting and the unbound type are unaffected.
		a.app.InvokeActor(t, ctx, a.plainType, "a1")
		code, body = a.app.SaveActorStateHTTP(t, ctx, a.plainType, "a1", "kh2", "v1")
		assert.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)
		require.NoError(t, a.app.SaveActorStateGRPC(t, ctx, a.plainType, "a1", "kg2", "v1"))
		got, err := a.app.GetActorStateGRPC(t, ctx, a.plainType, "a1", "kg2")
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
			code, body := a.app.ActorHTTP(c, ctx, nethttp.MethodGet, a.boundType, "a1", "/state/kh", "")
			assert.Equal(c, nethttp.StatusOK, code)
			assert.JSONEq(c, `"v1"`, body)
		}, 20*time.Second, 10*time.Millisecond)

		// No state call succeeded while the store was missing, so adding it
		// back has nothing stale to drop and deactivates no actor.
		assert.Equal(t, int64(1), a.boundDeactivated.Load())
		assert.Equal(t, int64(0), a.plainDeactivated.Load())

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			got, err := a.app.GetActorStateGRPC(t, ctx, a.boundType, "a1", "kg")
			if assert.NoError(c, err) {
				assert.Equal(c, "v1", got)
			}
		}, 20*time.Second, 10*time.Millisecond)

		assert.Equal(t, boundKeys, a.txDB.StateKeys(t, ctx))
	})

	t.Run("bound store reloaded in place against another database", func(t *testing.T) {
		// Activate the bound type's actor against the current database.
		a.app.InvokeActor(t, ctx, a.boundType, "a1")
		require.Equal(t, int64(1), a.boundDeactivated.Load())

		require.NoError(t, os.WriteFile(a.compFile, []byte(a.otherDB.GetComponent(t)), 0o600))

		// The actor is deactivated, so none keeps state read from the
		// previous database.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, int64(2), a.boundDeactivated.Load())
		}, 20*time.Second, 10*time.Millisecond)
		assert.Equal(t, int64(0), a.plainDeactivated.Load())

		// State operations use the new database: the previous keys are not
		// there, and new writes land only in it.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			code, _ := a.app.ActorHTTP(c, ctx, nethttp.MethodGet, a.boundType, "a1", "/state/kh", "")
			assert.Equal(c, nethttp.StatusNoContent, code)
		}, 20*time.Second, 10*time.Millisecond)
		got, err := a.app.GetActorStateGRPC(t, ctx, a.boundType, "a1", "kg")
		require.NoError(t, err)
		assert.Empty(t, got)

		a.app.InvokeActor(t, ctx, a.boundType, "a1")
		code, body := a.app.SaveActorStateHTTP(t, ctx, a.boundType, "a1", "kh3", "v1")
		require.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)
		require.NoError(t, a.app.SaveActorStateGRPC(t, ctx, a.boundType, "a1", "kg3", "v1"))

		assert.Equal(t, []string{
			appID + "||" + a.boundType + "||a1||kg3",
			appID + "||" + a.boundType + "||a1||kh3",
		}, a.otherDB.StateKeys(t, ctx))
		assert.Equal(t, boundKeys, a.txDB.StateKeys(t, ctx))
	})
}
