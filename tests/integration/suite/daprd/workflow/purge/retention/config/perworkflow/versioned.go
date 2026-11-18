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

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(versioned))
}

// versioned: a versioned registration is matched by the name the workflow
// is scheduled with, not by its version.
type versioned struct {
	workflow *workflow.Workflow
}

func (v *versioned) Setup(t *testing.T) []framework.Option {
	v.workflow = workflow.New(t, workflow.WithDaprdOptions(0, policy(t, `
      anyTerminal: "1h"
      workflows:
        - name: workflow
          completed: "1s"
`)))
	return []framework.Option{framework.WithProcesses(v.workflow)}
}

func (v *versioned) Run(t *testing.T, ctx context.Context) {
	v.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, v.workflow.Registry().AddVersionedWorkflowN("workflow", "v2", true, func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	cl := v.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, v.workflow)

	id, err := cl.ScheduleNewWorkflow(ctx, "workflow")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	waitPurged(t, rows, string(id), "the entry did not match the versioned workflow's name")
}
