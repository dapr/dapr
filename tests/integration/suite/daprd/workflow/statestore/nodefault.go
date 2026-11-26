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

package statestore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
)

func init() {
	suite.Register(new(nodefault))
}

// nodefault ensures that an app with only the state store bound by
// spec.workflow.stateStore loaded, and no default actor state store, cannot
// run workflows: hosting the workflow actor types needs the default actor
// state store, so the workflow operations fail with the error which asks for
// it, and nothing is written to the bound store.
type nodefault struct {
	workflow *workflow.Workflow
	wfDB     *sqlite.SQLite
}

func (n *nodefault) Setup(t *testing.T) []framework.Option {
	n.wfDB = sqlite.New(t,
		sqlite.WithName("wallet-workflows"),
		sqlite.WithCreateStateTables(),
	)
	n.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithResourceFiles(n.wfDB.GetComponent(t)),
			daprd.WithConfigManifests(t, workflowStateStoreConfig("wallet-workflows")),
		),
	)
	return []framework.Option{
		framework.WithProcesses(n.wfDB, n.workflow),
	}
}

func (n *nodefault) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	const noActorStore = "the state store is not configured to use the actor runtime. Have you set the - name: actorStateStore value: \"true\" in your state store component file?"

	// The workflow actor types are not hosted, so no worker is connected.
	cl := client.NewTaskHubGrpcClient(n.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))
	_, err := cl.ScheduleNewWorkflow(ctx, "foo")
	require.ErrorContains(t, err, noActorStore)
	require.ErrorContains(t, cl.RaiseEvent(ctx, "foo-instance", "event"), noActorStore)
	require.ErrorContains(t, cl.PurgeWorkflowState(ctx, "foo-instance"), noActorStore)

	_, err = n.workflow.WorkflowClient(t, ctx).ListInstanceIDs(ctx)
	require.ErrorContains(t, err, noActorStore)

	assert.Empty(t, n.wfDB.StateKeys(t, ctx))
}
