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

package statefulhistory

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(clientchurn))
}

// clientchurn drives a batch of in-flight workflows through a churn of workers
// repeatedly connecting and disconnecting against the same sidecar, always
// keeping at least one worker connected during the handover. Turns interrupted by
// a disconnect are redelivered to another worker, and each fresh worker is re-warmed
// from a full history before resuming deltas. Every workflow must still complete
// with the correct result.
type clientchurn struct {
	workflow *workflow.Workflow
}

func (c *clientchurn) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(c.workflow)}
}

func (c *clientchurn) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const (
		activityCount = 20
		workflowCount = 8
	)

	newRegistry := func() *task.TaskRegistry {
		reg := task.NewTaskRegistry()
		require.NoError(t, reg.AddWorkflowN("Accumulate", accumulate(activityCount)))
		require.NoError(t, reg.AddActivityN("AddOne", addOne))
		return reg
	}

	mgmt := c.workflow.ManagementClient(t, ctx)

	current := c.workflow.ConnectWorker(t, ctx, newRegistry())
	c.workflow.WaitForConnectedWorkers(t, ctx, 1)

	ids := make([]api.InstanceID, workflowCount)
	for i := range ids {
		id, err := mgmt.ScheduleNewWorkflow(ctx, "Accumulate")
		require.NoError(t, err)
		ids[i] = id
	}

	for round := 0; round < 4; round++ {
		next := c.workflow.ConnectWorker(t, ctx, newRegistry())
		c.workflow.WaitForConnectedWorkers(t, ctx, 2)
		current.Disconnect(t)
		c.workflow.WaitForConnectedWorkers(t, ctx, 1)
		current = next
	}

	for _, id := range ids {
		meta, err := mgmt.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
		assert.Equal(t, strconv.Itoa(activityCount), meta.GetOutput().GetValue())
	}
}
