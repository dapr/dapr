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
	suite.Register(new(nopropagation))
}

// nopropagation tests that without WithHistoryPropagation, neither child
// workflows nor activities receive propagated history. This verifies the
// opt-in default behavior- propagation only happens when explicitly requested.
type nopropagation struct {
	workflow *procworkflow.Workflow

	childHistoryReceived    atomic.Bool
	activityHistoryReceived atomic.Bool
}

func (n *nopropagation) Setup(t *testing.T) []framework.Option {
	n.workflow = procworkflow.New(t,
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *nopropagation) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	reg := n.workflow.Registry()

	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("activity").Await(nil); err != nil {
			return nil, err
		}

		var childResult string
		if err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInput("test"),
			// No WithHistoryPropagation — default is no propagation
		).Await(&childResult); err != nil {
			return nil, err
		}

		return childResult, nil
	})

	reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		if ph := ctx.GetPropagatedHistory(); ph != nil {
			n.childHistoryReceived.Store(true)
		}
		return statusDone, nil
	})

	reg.AddActivityN("activity", func(ctx task.ActivityContext) (any, error) {
		if ph := ctx.GetPropagatedHistory(); ph != nil {
			n.activityHistoryReceived.Store(true)
		}
		return nil, nil
	})

	client := n.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))

	// Neither target should have received propagated history
	assert.False(t, n.childHistoryReceived.Load(),
		"child workflow should NOT receive propagated history when parent doesn't opt in")
	assert.False(t, n.activityHistoryReceived.Load(),
		"activity should NOT receive propagated history when parent doesn't opt in")
}
