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

package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(defaultmaxbodysize))
}

// defaultmaxbodysize starts daprd without --max-body-size and runs a workflow
// whose history holds a 6 MiB activity result. With the old 4 MiB default,
// daprd stalled this workflow with PAYLOAD_SIZE_EXCEEDED.
type defaultmaxbodysize struct {
	workflow *workflow.Workflow
}

func (d *defaultmaxbodysize) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *defaultmaxbodysize) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	const payloadSize = 6 << 20
	largePayload := strings.Repeat("x", payloadSize)

	d.workflow.Registry().AddWorkflowN("large-history", func(ctx *task.WorkflowContext) (any, error) {
		var result string
		if err := ctx.CallActivity("produce-large-result").Await(&result); err != nil {
			return nil, err
		}
		return len(result), nil
	})

	d.workflow.Registry().AddActivityN("produce-large-result", func(ctx task.ActivityContext) (any, error) {
		return largePayload, nil
	})

	client := d.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "large-history")
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, metadata.GetRuntimeStatus())
	assert.Equal(t, "6291456", metadata.GetOutput().GetValue())
}
