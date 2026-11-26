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
	suite.Register(new(workflowstatestoreactivity))
}

// workflowstatestoreactivity ensures that the state store bound by
// spec.workflow.stateStore can be reloaded in place, against the same
// database, while an activity of a workflow is running: the activity
// finishes, and the workflow completes with its result from the state in the
// reloaded store.
type workflowstatestoreactivity struct {
	workflow *workflow.Workflow
	wfDB     *sqlite.SQLite
	logline  *logline.LogLine

	// compFile is the hot-reloadable component file of the bound store.
	compFile string
	compYAML string

	// started is closed when the activity starts, which returns once release
	// is closed.
	started chan struct{}
	release chan struct{}
}

func (w *workflowstatestoreactivity) Setup(t *testing.T) []framework.Option {
	w.started = make(chan struct{})
	w.release = make(chan struct{})

	w.wfDB = sqlite.New(t,
		sqlite.WithName("wallet-workflows"),
		sqlite.WithCreateStateTables(),
	)

	resDir := t.TempDir()
	w.compFile = filepath.Join(resDir, "wallet-workflows.yaml")
	w.compYAML = w.wfDB.GetComponent(t)
	require.NoError(t, os.WriteFile(w.compFile, []byte(w.compYAML), 0o600))

	w.logline = logline.New(t, logline.WithStdoutLineContains(
		"Bound actor state store wallet-workflows reloaded",
	))

	w.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithResourcesDir(resDir),
			daprd.WithLogLineStdout(w.logline),
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
		framework.WithProcesses(w.logline, w.wfDB, w.workflow),
	}
}

func (w *workflowstatestoreactivity) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	w.workflow.Registry().AddActivityN("blocked", func(ctx task.ActivityContext) (any, error) {
		close(w.started)
		select {
		case <-w.release:
		case <-ctx.Context().Done():
			return nil, ctx.Context().Err()
		}
		return "done", nil
	})
	w.workflow.Registry().AddWorkflowN("single", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallActivity("blocked").Await(&output)
		return output, err
	})

	client := w.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "single")
	require.NoError(t, err)
	select {
	case <-w.started:
	case <-ctx.Done():
		require.Fail(t, "the activity did not start")
	}

	// The bound store is reloaded in place, against the same database, while
	// the activity is running.
	reloaded := strings.Replace(w.compYAML, `"metadata":[`, `"metadata":[{"name":"busyTimeout","value":"5s"},`, 1)
	require.NotEqual(t, w.compYAML, reloaded)
	require.NoError(t, os.WriteFile(w.compFile, []byte(reloaded), 0o600))
	w.logline.EventuallyFoundAll(t)

	// The activity finishes, and the workflow completes with its result.
	close(w.release)
	meta, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())

	assert.Contains(t, strings.Join(w.wfDB.StateKeys(t, ctx), "\n"), "||"+string(id)+"||")
	// The default actor state store holds no workflow state.
	for _, key := range w.workflow.DB().StateKeys(t, ctx) {
		assert.NotContains(t, key, "||dapr.internal.")
	}
}
