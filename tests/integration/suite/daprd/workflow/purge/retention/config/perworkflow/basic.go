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
	suite.Register(new(basic))
}

const basicPolicy = `
      anyTerminal: "1h"
      workflows:
        - name: fast
          completed: "2s"
        - name: slow
          anyTerminal: "4s"
`

// basic: a per-workflow status TTL or anyTerminal overrides the app-wide
// policy for that name only.
type basic struct {
	workflow *workflow.Workflow
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t, workflow.WithDaprdOptions(0, policy(t, basicPolicy)))
	return []framework.Option{framework.WithProcesses(b.workflow)}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	runBasic(t, ctx, b.workflow)
}

func runBasic(t *testing.T, ctx context.Context, w *workflow.Workflow) {
	w.WaitUntilRunning(t, ctx)

	reg := w.Registry()
	for _, name := range []string{"fast", "other", "slow"} {
		require.NoError(t, reg.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity("bar").Await(nil)
		}))
	}
	require.NoError(t, reg.AddActivityN("bar", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	cl := w.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, w)

	run := func(name string) string {
		id, err := cl.ScheduleNewWorkflow(ctx, name)
		require.NoError(t, err)
		_, err = cl.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		return string(id)
	}

	fast := run("fast")
	other := run("other")
	slow := run("slow")

	// slow's 4s window starts at the completion observed just now.
	assert.Positive(t, rows(t, slow), "slow purged before its per-workflow anyTerminal TTL")
	waitPurged(t, rows, fast, "per-workflow completed TTL did not purge fast")
	waitPurged(t, rows, slow, "per-workflow anyTerminal TTL did not purge slow")

	// other completed before slow, so a 4s TTL wrongly applied to it would
	// have fired by now; only the app-wide 1h policy keeps it.
	assert.Positive(t, rows(t, other), "other purged despite having no per-workflow entry")
}
