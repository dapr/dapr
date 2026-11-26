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

package selfhosted

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	suite.Register(new(workflowstatestore))
}

// workflowstatestore ensures that hot reloading the state store bound by
// spec.workflow.stateStore is handled for the workflow actor types: while the
// store is removed, workflow operations fail with an error naming it, and
// once it is added back a running workflow continues from the state already
// in it.
type workflowstatestore struct {
	workflow *workflow.Workflow
	wfDB     *sqlite.SQLite

	// compFile is the hot-reloadable component file of the bound store.
	compFile string
	compYAML string
}

func (w *workflowstatestore) Setup(t *testing.T) []framework.Option {
	w.wfDB = sqlite.New(t,
		sqlite.WithName("wallet-workflows"),
		sqlite.WithCreateStateTables(),
	)

	resDir := t.TempDir()
	w.compFile = filepath.Join(resDir, "wallet-workflows.yaml")
	w.compYAML = w.wfDB.GetComponent(t)
	require.NoError(t, os.WriteFile(w.compFile, []byte(w.compYAML), 0o600))

	w.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithResourcesDir(resDir),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowstatestore
spec:
  workflow:
    stateStore: wallet-workflows
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(w.wfDB, w.workflow),
	}
}

func (w *workflowstatestore) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	w.workflow.Registry().AddWorkflowN("waiter", func(ctx *task.WorkflowContext) (any, error) {
		var payload string
		err := ctx.WaitForSingleEvent("go", -1).Await(&payload)
		return payload, err
	})

	client := w.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "waiter")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Contains(t, strings.Join(w.wfDB.StateKeys(t, ctx), "\n"), "||"+string(id)+"||")

	const notLoaded = "is bound to state store wallet-workflows, which is not loaded"

	// Removing the bound store fails the workflow operations. Listing is
	// polled until the removal is applied, as it changes nothing.
	require.NoError(t, os.Remove(w.compFile))
	wf := w.workflow.WorkflowClient(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, lerr := wf.ListInstanceIDs(ctx)
		assert.ErrorContains(c, lerr, notLoaded)
	}, 20*time.Second, 10*time.Millisecond)
	require.ErrorContains(t, client.RaiseEvent(ctx, id, "go", api.WithEventPayload("removed")), notLoaded)

	// Adding it back lets the workflow continue from its state.
	require.NoError(t, os.WriteFile(w.compFile, []byte(w.compYAML), 0o600))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, client.RaiseEvent(ctx, id, "go", api.WithEventPayload("done")))
	}, 20*time.Second, 10*time.Millisecond)

	meta, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())

	// The default actor state store holds no workflow state.
	for _, key := range w.workflow.DB().StateKeys(t, ctx) {
		assert.NotContains(t, key, "||dapr.internal.")
	}
}
