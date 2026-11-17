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

package orderingkey

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/workflow/runs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(continueasnew))
}

// continueasnew checks that an instance holds its key across ContinueAsNew.
type continueasnew struct {
	workflow *workflow.Workflow
}

func (x *continueasnew) Setup(t *testing.T) []framework.Option {
	x.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(x.workflow),
	}
}

func (x *continueasnew) Run(t *testing.T, ctx context.Context) {
	x.workflow.WaitUntilRunning(t, ctx)
	var r runs.Recorder
	x.workflow.Registry().AddWorkflowN("wait", r.Waiter)
	x.workflow.Registry().AddWorkflowN("loop", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		ctx.SetCustomStatus(strconv.Itoa(gen))
		if err := ctx.WaitForSingleEvent("go", -1).Await(nil); err != nil {
			return nil, err
		}
		if gen == 0 {
			ctx.ContinueAsNew(1)
		}
		return nil, nil
	})
	cl := x.workflow.BackendClient(t, ctx)

	key := api.WithOrdering(api.Ordering{Key: "loop"})
	_, err := cl.ScheduleNewWorkflow(ctx, "loop", api.WithInstanceID("a"), api.WithInput(0), key)
	require.NoError(t, err)
	runs.Start(t, ctx, cl, "b", key)

	require.NoError(t, cl.RaiseEvent(ctx, "a", "go"))
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		md, err := cl.FetchWorkflowMetadata(ctx, "a")
		assert.NoError(c, err)
		assert.Equal(c, "1", md.GetCustomStatus().GetValue())
	}, 20*time.Second, 10*time.Millisecond)
	r.AssertQueued(t, "b")

	r.Finish(t, ctx, cl, "a")
	r.AssertStarted(t, "b")
	r.Finish(t, ctx, cl, "b")
}
