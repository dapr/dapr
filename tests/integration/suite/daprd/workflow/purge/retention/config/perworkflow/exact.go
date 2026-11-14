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
	suite.Register(new(exact))
}

// exact: names match exactly and case-sensitively. Without an app-wide
// policy a non-matching name queues no retention reminder at all.
type exact struct {
	workflow *workflow.Workflow
}

func (e *exact) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t, workflow.WithDaprdOptions(0, policy(t, `
      workflows:
        - name: Fast
          completed: "1s"
`)))
	return []framework.Option{framework.WithProcesses(e.workflow)}
}

func (e *exact) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	reg := e.workflow.Registry()
	for _, name := range []string{"fast", "Fast"} {
		require.NoError(t, reg.AddWorkflowN(name, func(*task.WorkflowContext) (any, error) {
			return nil, nil
		}))
	}
	cl := e.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, e.workflow)

	lower, err := cl.ScheduleNewWorkflow(ctx, "fast")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, lower)
	require.NoError(t, err)

	upper, err := cl.ScheduleNewWorkflow(ctx, "Fast")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, upper)
	require.NoError(t, err)
	waitPurged(t, rows, string(upper), "the exact-match entry did not purge")

	assert.Positive(t, rows(t, string(lower)), "a case-different name must not match the entry")
	assert.Empty(t, retentionJobs(t, ctx, e.workflow), "no reminder may be queued for a name without a TTL")
}
