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

package dedup

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(continueasnew))
}

type continueasnew struct {
	workflow *workflow.Workflow
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const iterations = 5
	var activityCalls atomic.Int32

	c.workflow.Registry().AddWorkflowN("dedup-continueasnew", func(ctx *task.WorkflowContext) (any, error) {
		var iter int
		require.NoError(t, ctx.GetInput(&iter))

		if err := ctx.CallActivity("ping").Await(nil); err != nil {
			return nil, err
		}

		if iter+1 >= iterations {
			return iter + 1, nil
		}
		ctx.ContinueAsNew(iter + 1)
		return nil, nil
	})
	require.NoError(t, c.workflow.Registry().AddActivityN("ping", func(ctx task.ActivityContext) (any, error) {
		activityCalls.Add(1)
		return nil, nil
	}))

	cl := c.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "dedup-continueasnew")
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())

	time.Sleep(time.Second * 2)
	assert.Equal(t, int32(iterations), activityCalls.Load(),
		"activity must run once per ContinueAsNew iteration; same EventId across generations must not be skipped")
}
