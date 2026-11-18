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
package perworkflow

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
	suite.Register(new(restart))
}

// restart: the reminder queued with an entry's TTL survives a daprd restart
// and purges once a worker is back.
type restart struct {
	workflow *workflow.Workflow
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t, workflow.WithDaprdOptions(0, policy(t, `
      anyTerminal: "1h"
      workflows:
        - name: fast
          completed: "5s"
`)))
	return []framework.Option{framework.WithProcesses(r.workflow)}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.Registry().AddWorkflowN("fast", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	cl := r.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, r.workflow)

	id, err := cl.ScheduleNewWorkflow(ctx, "fast")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Positive(t, rows(t, string(id)))

	r.workflow.Dapr().Restart(t, ctx)
	r.workflow.WaitUntilRunning(t, ctx)
	r.workflow.BackendClient(t, ctx)

	waitPurged(t, rows, string(id), "the entry's reminder did not purge after the restart")
}
