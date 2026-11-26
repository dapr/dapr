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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(existing))
}

// existing ensures that setting spec.workflow.stateStore on an app which
// already has workflows does not migrate them: after the restart, a workflow
// started on the default actor state store is not found, its state stays
// untouched in that store, and new workflows use the bound store. daprd logs
// that the state is not migrated.
type existing struct {
	workflow *workflow.Workflow
	wfDB     *sqlite.SQLite
	logline  *logline.LogLine
}

func (e *existing) Setup(t *testing.T) []framework.Option {
	e.wfDB = sqlite.New(t,
		sqlite.WithName("wallet-workflows"),
		sqlite.WithCreateStateTables(),
	)
	e.logline = logline.New(t, logline.WithStdoutLineContains(
		"Workflow state is stored in state store wallet-workflows, set by spec.workflow.stateStore. State is not migrated: workflows started with another state store are not found",
	))
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.logline, e.wfDB, e.workflow),
	}
}

func (e *existing) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("waiter", func(ctx *task.WorkflowContext) (any, error) {
		var payload string
		err := ctx.WaitForSingleEvent("go", -1).Await(&payload)
		return payload, err
	}))

	// A workflow is in progress on the default actor state store.
	before := e.workflow.BackendClientFor(t, ctx, e.workflow.Dapr(), registry)
	id, err := before.ScheduleNewWorkflow(ctx, "waiter")
	require.NoError(t, err)
	_, err = before.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	defaultKeys := e.workflow.DB().StateKeys(t, ctx)
	require.Contains(t, strings.Join(defaultKeys, "\n"), "||"+string(id)+"||")

	// The app restarts with its workflow actor types bound to another store.
	e.workflow.Dapr().Kill(t)
	bound := e.workflow.NewDaprd(t,
		daprd.WithAppID(e.workflow.Dapr().AppID()),
		daprd.WithResourceFiles(e.wfDB.GetComponent(t)),
		daprd.WithConfigManifests(t, workflowStateStoreConfig("wallet-workflows")),
		daprd.WithLogLineStdout(e.logline),
	)
	bound.Run(t, ctx)
	t.Cleanup(func() { bound.Cleanup(t) })
	bound.WaitUntilRunning(t, ctx)
	after := e.workflow.BackendClientFor(t, ctx, bound, registry)
	e.logline.EventuallyFoundAll(t)

	// The workflow in progress is not found, and cannot be continued.
	_, err = after.FetchWorkflowMetadata(ctx, id)
	require.ErrorContains(t, err, "no such instance exists")
	require.ErrorContains(t, after.RaiseEvent(ctx, id, "go", api.WithEventPayload("late")), "no such instance exists")

	// Its state is not migrated: it stays as it was in the default actor
	// state store, and the bound store has none of it.
	assert.Equal(t, defaultKeys, e.workflow.DB().StateKeys(t, ctx))
	assert.Empty(t, e.wfDB.StateKeys(t, ctx))

	// New workflows use the bound store.
	newID, err := after.ScheduleNewWorkflow(ctx, "waiter")
	require.NoError(t, err)
	_, err = after.WaitForWorkflowStart(ctx, newID)
	require.NoError(t, err)
	require.NoError(t, after.RaiseEvent(ctx, newID, "go", api.WithEventPayload("done")))
	meta, err := after.WaitForWorkflowCompletion(ctx, newID, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
	assert.Contains(t, strings.Join(e.wfDB.StateKeys(t, ctx), "\n"), "||"+string(newID)+"||")
	assert.Equal(t, defaultKeys, e.workflow.DB().StateKeys(t, ctx))
}
