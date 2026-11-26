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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(list))
}

// list ensures that listing the workflow instance IDs reads the state store
// bound by spec.workflow.stateStore, and that purging an instance deletes its
// state from that store.
type list struct {
	workflow *workflow.Workflow
	wfDB     *sqlite.SQLite
}

func (l *list) Setup(t *testing.T) []framework.Option {
	l.wfDB = sqlite.New(t,
		sqlite.WithName("wallet-workflows"),
		sqlite.WithCreateStateTables(),
	)

	l.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithResourceFiles(l.wfDB.GetComponent(t)),
			daprd.WithConfigManifests(t, workflowStateStoreConfig("wallet-workflows")),
		),
	)

	return []framework.Option{
		framework.WithProcesses(l.wfDB, l.workflow),
	}
}

func (l *list) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	l.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})

	client := l.workflow.BackendClient(t, ctx)
	ids := make([]string, 0, 2)
	for range 2 {
		id, err := client.ScheduleNewWorkflow(ctx, "foo")
		require.NoError(t, err)
		_, err = client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		ids = append(ids, id.String())
	}

	// The instances exist only in the bound store, and are listed from it.
	wf := l.workflow.WorkflowClient(t, ctx)
	resp, err := wf.ListInstanceIDs(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, ids, resp.InstanceIds)

	// Purging an instance deletes its state from the bound store.
	require.NoError(t, wf.PurgeWorkflowState(ctx, ids[0]))
	for _, key := range l.wfDB.StateKeys(t, ctx) {
		assert.NotContains(t, key, "||"+ids[0]+"||")
	}
	resp, err = wf.ListInstanceIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, ids[1:], resp.InstanceIds)

	// The default actor state store holds no workflow state.
	for _, key := range l.workflow.DB().StateKeys(t, ctx) {
		assert.NotContains(t, key, "||dapr.internal.")
	}
}
