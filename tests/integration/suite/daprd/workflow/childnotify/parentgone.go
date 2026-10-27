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

package childnotify

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(parentgone))
}

// parentgone verifies a child whose parent was force purged, or terminated
// without recursion, still completes and does not loop re-sending.
type parentgone struct {
	workflow *workflow.Workflow
}

func (p *parentgone) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(p.workflow)}
}

func (p *parentgone) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	reg := p.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return "late", ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var childID string
		if err := ctx.GetInput(&childID); err != nil {
			return nil, err
		}
		return nil, ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(childID)).Await(nil)
	}))
	cl := p.workflow.BackendClient(t, ctx)
	sched := p.workflow.Scheduler()

	for name, remove := range map[string]func(t *testing.T, id api.InstanceID){
		"purged": func(t *testing.T, id api.InstanceID) {
			require.NoError(t, cl.PurgeWorkflowState(ctx, id, api.WithForcePurge(true)))
		},
		"terminated": func(t *testing.T, id api.InstanceID) {
			require.NoError(t, cl.TerminateWorkflow(ctx, id, api.WithRecursiveTerminate(false)))
			wf.WaitForRuntimeStatus(t, ctx, cl, id, api.RUNTIME_STATUS_TERMINATED)
		},
	} {
		t.Run(name, func(t *testing.T) {
			childID := "parentgone-" + name
			id, err := cl.ScheduleNewWorkflow(ctx, "parent", api.WithInput(childID))
			require.NoError(t, err)
			wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID(childID), api.RUNTIME_STATUS_RUNNING)

			remove(t, id)

			require.NoError(t, cl.RaiseEvent(ctx, api.InstanceID(childID), "go"))
			wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID(childID), api.RUNTIME_STATUS_COMPLETED)

			// Give a retry loop time to show itself before asserting there is none.
			time.Sleep(time.Second * 2)
			assert.Zero(t, sched.JobKeyCount(t, ctx, "parent-notify"), "an absent parent must be treated as delivered")
		})
	}
}
