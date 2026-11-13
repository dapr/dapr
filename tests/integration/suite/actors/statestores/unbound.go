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
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
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
		for _, actorType := range actorTypes {
			u.app.SaveAndReadActorStateHTTP(t, ctx, actorType, "h1")
		}
	})

	t.Run("grpc", func(t *testing.T) {
		for _, actorType := range actorTypes {
			u.app.SaveAndReadActorStateGRPC(t, ctx, actorType, "g1")
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
