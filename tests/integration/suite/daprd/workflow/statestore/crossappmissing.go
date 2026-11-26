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

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossappmissing))
}

// crossappmissing ensures that a missing state store bound to app0's workflow
// actor types blocks only app0's own workflows: the workflow operations app0
// sends to app1, which uses its own store, still succeed.
type crossappmissing struct {
	workflow *workflow.Workflow
}

func (c *crossappmissing) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0,
			daprd.WithConfigManifests(t, workflowStateStoreConfig("wallet-workflows")),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossappmissing) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	app1 := c.workflow.DaprN(1).AppID()

	c.workflow.Registry().AddWorkflowN("waiter", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	c.workflow.RegistryN(1).AddWorkflowN("waiter", func(ctx *task.WorkflowContext) (any, error) {
		var payload string
		err := ctx.WaitForSingleEvent("go", -1).Await(&payload)
		return payload, err
	})

	client0 := c.workflow.BackendClient(t, ctx)
	client1 := c.workflow.BackendClientN(t, ctx, 1)

	// app0's own workflows need its bound store.
	_, err := client0.ScheduleNewWorkflow(ctx, "waiter")
	require.ErrorContains(t, err, "is bound to state store wallet-workflows, which is not loaded")

	// Operations app0 sends to app1 do not.
	id, err := client0.ScheduleNewWorkflow(ctx, "waiter", api.WithAppID(app1))
	require.NoError(t, err)
	_, err = client1.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.NoError(t, client0.RaiseEvent(ctx, id, "go", api.WithEventPayload("done"), api.WithRaiseEventAppID(app1)))
	meta, err := client1.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))
	require.Equal(t, `"done"`, meta.GetOutput().GetValue())

	require.NoError(t, client0.PurgeWorkflowState(ctx, id, api.WithPurgeAppID(app1)))
}
