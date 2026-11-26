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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unhosted))
}

// unhosted ensures that daprd warns, once the app has registered its actor
// types, about a bound actor type that the app does not host, since the
// binding then has no effect. A binding that differs from a hosted actor type
// only in case names the hosted type, as actor types are case-sensitive.
type unhosted struct {
	app     *actors.Actors
	logline *logline.LogLine
}

func (u *unhosted) Setup(t *testing.T) []framework.Option {
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	u.logline = logline.New(t, logline.WithStdoutLineContains(
		"Actor type transactionactor is bound to state store wallet-transactions, but this app does not host it; did you mean TransactionActor? Actor types are case-sensitive",
		"Actor type OrphanActor is bound to state store wallet-orphans, but this app does not host it; actor state calls this app makes for that type use the bound store",
	))

	u.app = actors.New(t,
		actors.WithDB(db),
		actors.WithActorTypes("TransactionActor"),
		actors.WithActorTypeHandler("TransactionActor", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithDaprdOptions(
			daprd.WithLogLineStdout(u.logline),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: wallet-transactions
      actorTypes: ["transactionactor"]
    - name: wallet-orphans
      actorTypes: ["OrphanActor"]
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(u.logline, u.app),
	}
}

func (u *unhosted) Run(t *testing.T, ctx context.Context) {
	u.app.WaitUntilRunning(t, ctx)
	u.logline.EventuallyFoundAll(t)
}
