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

package list

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(continueasnew))
}

// continueasnew lists an instance across ContinueAsNew generations. The list
// never shows CONTINUED_AS_NEW, and createdAt moves to each new generation,
// the same as GetInstance.
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

	c.workflow.Registry().AddWorkflowN("loop", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		if err := ctx.WaitForSingleEvent("next", time.Minute).Await(nil); err != nil {
			return nil, err
		}
		if gen < 2 {
			ctx.ContinueAsNew(gen + 1)
			return nil, nil
		}
		return gen, nil
	})

	client := c.workflow.BackendClient(t, ctx)

	const id = "can-1"
	_, err := client.ScheduleNewWorkflow(ctx, "loop", api.WithInstanceID(id), api.WithInput(0))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	canFilter := api.WithListInstancesRuntimeStatus(api.RUNTIME_STATUS_CONTINUED_AS_NEW)

	// running waits until the instance runs a generation created after
	// prev, and returns its summary.
	running := func(prev time.Time) *protos.WorkflowInstanceSummary {
		var sum *protos.WorkflowInstanceSummary
		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			resp, lerr := client.ListInstances(ctx)
			if !assert.NoError(ct, lerr) || !assert.Len(ct, resp.GetInstances(), 1) {
				return
			}
			got := resp.GetInstances()[0]
			// Every observation, also the ones before the new generation
			// runs, has a status other than CONTINUED_AS_NEW.
			assert.NotEqual(t, api.RUNTIME_STATUS_CONTINUED_AS_NEW, got.GetRuntimeStatus())
			assert.Equal(ct, api.RUNTIME_STATUS_RUNNING, got.GetRuntimeStatus())
			assert.True(ct, got.GetCreatedTimestamp().AsTime().After(prev))
			sum = got
		}, 5*time.Second, 5*time.Millisecond)
		require.NotNil(t, sum)
		assert.Empty(t, fworkflow.ListedIDs(t, ctx, client, canFilter))
		fworkflow.AssertSummaryMatchesMetadata(t, ctx, client, sum)
		return sum
	}

	gen0 := running(time.Time{})
	require.NoError(t, client.RaiseEvent(ctx, id, "next"))
	gen1 := running(gen0.GetCreatedTimestamp().AsTime())
	require.NoError(t, client.RaiseEvent(ctx, id, "next"))
	running(gen1.GetCreatedTimestamp().AsTime())
	require.NoError(t, client.RaiseEvent(ctx, id, "next"))

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	all := fworkflow.ListAllInstances(t, ctx, client)
	require.Len(t, all, 1)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, all[0].GetRuntimeStatus())
	fworkflow.AssertSummaryMatchesMetadata(t, ctx, client, all[0])
	assert.Empty(t, fworkflow.ListedIDs(t, ctx, client, canFilter))
}
