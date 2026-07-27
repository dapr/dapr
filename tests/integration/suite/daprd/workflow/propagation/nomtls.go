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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(nomtls))
}

// nomtls verifies that history propagation works in standalone mode without
// mTLS / signing, and that the orchestrator logs a warning at every dispatch
// so operators are aware the propagated chunks are unsigned. This unblocks
// standalone deployments where mTLS is a pain to configure while still
// surfacing the integrity-cannot-be-verified state in logs.
type nomtls struct {
	workflow *procworkflow.Workflow
	logline  *logline.LogLine

	childHistoryReceived atomic.Bool
}

func (n *nomtls) Setup(t *testing.T) []framework.Option {
	n.logline = logline.New(t,
		logline.WithStdoutLineContains("propagating unsigned workflow history"),
	)

	n.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(1),
		procworkflow.WithDaprdOptions(0, daprd.WithLogLineStdout(n.logline)),
	)

	return []framework.Option{
		framework.WithProcesses(n.logline, n.workflow),
	}
}

func (n *nomtls) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	reg := n.workflow.Registry()

	reg.AddWorkflowN("parentWf", func(ctx *task.WorkflowContext) (any, error) {
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
		return statusNoHistoryHyphen, nil
	})

	client := n.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parentWf")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(metadata))

	// Propagation works even without mTLS / signing.
	assert.True(t, n.childHistoryReceived.Load(),
		"child should receive propagated history even without mTLS / signing")
	assert.Contains(t, metadata.GetOutput().GetValue(), "has-history",
		"child should observe propagated history (chunks are still emitted, just unsigned)")
	n.logline.EventuallyFoundAll(t)
}
