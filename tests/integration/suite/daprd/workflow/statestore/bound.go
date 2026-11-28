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
	"fmt"
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
	suite.Register(new(bound))
}

// bound ensures that spec.workflow.stateStore puts the state of the app's
// workflow and activity actors in the bound state store, and none in the
// default actor state store.
type bound struct {
	workflow *workflow.Workflow
	wfDB     *sqlite.SQLite
}

func (b *bound) Setup(t *testing.T) []framework.Option {
	b.wfDB = sqlite.New(t,
		sqlite.WithName("wallet-workflows"),
		sqlite.WithCreateStateTables(),
	)

	b.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithResourceFiles(b.wfDB.GetComponent(t)),
			daprd.WithConfigManifests(t, workflowStateStoreConfig("wallet-workflows")),
		),
	)

	return []framework.Option{
		framework.WithProcesses(b.wfDB, b.workflow),
	}
}

func (b *bound) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	b.workflow.Registry().AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		var name string
		if err := ctx.GetInput(&name); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", name), nil
	})
	b.workflow.Registry().AddWorkflowN("SingleActivity", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
		return output, err
	})

	client := b.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "SingleActivity", api.WithInput("Dapr"))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())

	// The workflow state is in the bound store, under the internal workflow
	// actor types.
	boundKeys := b.wfDB.StateKeys(t, ctx)
	require.NotEmpty(t, boundKeys)
	for _, key := range boundKeys {
		assert.Contains(t, key, "||dapr.internal.")
	}
	assert.Contains(t, strings.Join(boundKeys, "\n"), "||"+string(id)+"||")

	// The default actor state store holds no workflow state.
	for _, key := range b.workflow.DB().StateKeys(t, ctx) {
		assert.NotContains(t, key, "||dapr.internal.")
	}
}

// workflowStateStoreConfig returns a Configuration which binds the app's
// workflow actor types to the state store storeName.
func workflowStateStoreConfig(storeName string) string {
	return `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowstatestore
spec:
  workflow:
    stateStore: ` + storeName + `
`
}
