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
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unhostedsuspended))
}

// unhostedsuspended ensures that the warning about bound actor types the app
// does not host compares against the actor types the app registers, also
// while actor hosting is suspended because no default actor state store is
// loaded: a registered bound actor type is not reported.
type unhostedsuspended struct {
	app     *actors.Actors
	logline *logline.LogLine
}

func (u *unhostedsuspended) Setup(t *testing.T) []framework.Option {
	// The only state store is the bound one, so there is no default actor
	// state store and hosting stays suspended.
	db := sqlite.New(t,
		sqlite.WithName("wallet-balance"),
		sqlite.WithActorStateStore(false),
		sqlite.WithCreateStateTables(),
	)

	u.logline = logline.New(t, logline.WithCaptureAll())

	u.app = actors.New(t,
		actors.WithDB(db),
		actors.WithActorTypes("BalanceActor"),
		actors.WithActorTypeHandler("BalanceActor", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithDaprdOptions(
			daprd.WithExecOptions(exec.WithStdout(u.logline.Stdout()), exec.WithStderr(u.logline.Stderr())),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: wallet-balance
      actorTypes: ["BalanceActor", "OrphanActor"]
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(u.logline, u.app),
	}
}

func (u *unhostedsuspended) Run(t *testing.T, ctx context.Context) {
	u.app.Daprd().WaitUntilRunning(t, ctx)

	// The warning for the unregistered type shows the app's types were
	// registered and compared.
	u.logline.EventuallyContains(t,
		"Actor type OrphanActor is bound to state store wallet-balance, but this app does not host it",
		time.Second*20, time.Millisecond*10)
	assert.False(t, u.logline.Contains("Actor type BalanceActor is bound to state store wallet-balance, but this app does not host it"))
}
