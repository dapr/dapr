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

package selecttask

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(notasks))
}

// notasks calls Select with no candidates. Select returns an error instead
// of blocking the workflow forever.
type notasks struct {
	workflow *workflow.Workflow
}

func (n *notasks) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *notasks) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	n.workflow.Registry().AddWorkflowN("notasks", func(ctx *task.WorkflowContext) (any, error) {
		return ctx.Select()
	})

	cl := n.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "notasks")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_FAILED", meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetFailureDetails().GetErrorMessage(), "requires at least one task")
}
