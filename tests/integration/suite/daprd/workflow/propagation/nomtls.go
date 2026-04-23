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

package propagation

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(nomtls))
}

// nomtls tests that history propagation is blocked when mTLS is disabled.
// Even though the workflow configures WithHistoryPropagation(PropagateLineage()),
// the child should receive nil propagated history because w/o mTLS the
// runtime cannot sign/verify the history chain.
type nomtls struct {
	workflow *procworkflow.Workflow

	childHistoryReceived atomic.Bool
}

func (n *nomtls) Setup(t *testing.T) []framework.Option {
	n.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(1),
	)
	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *nomtls) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	reg := n.workflow.Registry()

	reg.AddActivityN("parentAct", func(ctx task.ActivityContext) (any, error) {
		return "done", nil
	})

	reg.AddWorkflowN("parentWf", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("parentAct").Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("childWf",
			task.WithChildWorkflowInput("test"),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})

	reg.AddWorkflowN("childWf", func(ctx *task.WorkflowContext) (any, error) {
		ph := ctx.GetPropagatedHistory()
		if ph != nil {
			n.childHistoryReceived.Store(true)
			return "has-history", nil
		}
		return "no-history", nil
	})

	client := n.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parentWf")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	// Child should NOT have received propagated history because mTLS is disabled
	assert.False(t, n.childHistoryReceived.Load(),
		"child should NOT receive propagated history when mTLS is disabled")
	assert.Contains(t, metadata.GetOutput().GetValue(), "no-history",
		"child should return 'no-history' since propagation is blocked without mTLS")
}
