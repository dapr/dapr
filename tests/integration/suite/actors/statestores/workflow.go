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
	"fmt"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	dtapi "github.com/dapr/durabletask-go/api"
	dtclient "github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflow))
}

// workflow ensures that actor state store bindings do not affect workflows:
// the workflow and activity actors keep their state in the default actor
// state store, while a bound actor type of the same app uses its bound store,
// which gets no workflow state.
type workflow struct {
	app  *actors.Actors
	txDB *sqlite.SQLite

	boundType string
}

func (w *workflow) Setup(t *testing.T) []framework.Option {
	w.boundType = "LedgerActor"

	w.txDB = sqlite.New(t,
		sqlite.WithName("wallet-transactions"),
		sqlite.WithCreateStateTables(),
	)

	w.app = actors.New(t,
		actors.WithActorTypes(w.boundType),
		actors.WithActorTypeHandler(w.boundType, func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithResources(w.txDB.GetComponent(t)),
		actors.WithDaprdOptions(daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: wallet-transactions
      actorTypes: ["`+w.boundType+`"]
`)),
	)

	return []framework.Option{
		framework.WithProcesses(w.txDB, w.app),
	}
}

func (w *workflow) Run(t *testing.T, ctx context.Context) {
	w.app.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		var name string
		if err := ctx.GetInput(&name); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", name), nil
	}))
	require.NoError(t, reg.AddWorkflowN("SingleActivity", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
		return output, err
	}))

	wfClient := dtclient.NewTaskHubGrpcClient(w.app.Daprd().GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, wfClient.StartWorkItemListener(ctx, reg))

	id, err := wfClient.ScheduleNewWorkflow(ctx, "SingleActivity", dtapi.WithInput("Dapr"))
	require.NoError(t, err)
	meta, err := wfClient.WaitForWorkflowCompletion(ctx, id, dtapi.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, dtapi.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())

	// The bound type still uses its bound store.
	saveAndReadGRPC(t, ctx, w.app, w.boundType, "g1")

	// The workflow state is in the default actor state store, under the
	// internal workflow actor types only.
	defaultKeys := w.app.DB().StateKeys(t, ctx)
	require.NotEmpty(t, defaultKeys)
	for _, key := range defaultKeys {
		assert.Contains(t, key, "||dapr.internal.")
	}
	assert.Contains(t, strings.Join(defaultKeys, "\n"), string(id))

	// The bound store holds only the bound type's state.
	assert.Equal(t, []string{
		w.app.AppID() + "||" + w.boundType + "||g1||k1",
	}, w.txDB.StateKeys(t, ctx))
}
