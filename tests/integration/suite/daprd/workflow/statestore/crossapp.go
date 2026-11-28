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
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossapp))
}

// crossapp ensures that spec.workflow.stateStore binds only the local app's
// workflow actor types: when app0, bound to its own store, runs a child
// workflow on app1, which is not bound, app0's workflow state is only in the
// bound store and app1's only in the default actor state store.
type crossapp struct {
	workflow *workflow.Workflow
	wfDB     *sqlite.SQLite
}

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	c.wfDB = sqlite.New(t,
		sqlite.WithName("wallet-workflows"),
		sqlite.WithCreateStateTables(),
	)

	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0,
			daprd.WithResourceFiles(c.wfDB.GetComponent(t)),
			daprd.WithConfigManifests(t, workflowStateStoreConfig("wallet-workflows")),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.wfDB, c.workflow),
	}
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	app0 := c.workflow.DaprN(0).AppID()
	app1 := c.workflow.DaprN(1).AppID()

	c.workflow.Registry().AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(app1),
			task.WithChildWorkflowInstanceID("child"),
		).Await(&output)
		return output, err
	})
	c.workflow.RegistryN(1).AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		return "from app1", nil
	})

	client0 := c.workflow.BackendClient(t, ctx)
	c.workflow.BackendClientN(t, ctx, 1)

	id, err := client0.ScheduleNewWorkflow(ctx, "Parent", api.WithInstanceID("parent"))
	require.NoError(t, err)
	meta, err := client0.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"from app1"`, meta.GetOutput().GetValue())

	// app0's workflow state is only in its bound store.
	boundKeys := c.wfDB.StateKeys(t, ctx)
	assert.Contains(t, strings.Join(boundKeys, "\n"), app0+"||dapr.internal.default."+app0+".workflow||parent||")
	for _, key := range boundKeys {
		assert.True(t, strings.HasPrefix(key, app0+"||"), "unexpected key in the bound store: %s", key)
	}

	// app1's workflow state is only in the default actor state store, which
	// holds none of app0's.
	defaultKeys := c.workflow.DB().StateKeys(t, ctx)
	assert.Contains(t, strings.Join(defaultKeys, "\n"), app1+"||dapr.internal.default."+app1+".workflow||child||")
	for _, key := range defaultKeys {
		assert.False(t, strings.HasPrefix(key, app0+"||dapr.internal."), "app0 workflow state in the default store: %s", key)
	}
}
