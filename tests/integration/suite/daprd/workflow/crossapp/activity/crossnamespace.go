/*
Copyright 2025 The Dapr Authors
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

package activity

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crossnamespace))
}

type crossnamespace struct {
	workflow             *workflow.Workflow
	actorNotFoundLogLine *logline.LogLine
}

func (c *crossnamespace) Setup(t *testing.T) []framework.Option {
	c.actorNotFoundLogLine = logline.New(t,
		logline.WithStdoutLineContains(
			"failed to lookup actor: api error: code = FailedPrecondition desc = did not find address for actor",
		),
	)

	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithNamespace("default"),
			daprd.WithExecOptions(
				exec.WithStdout(c.actorNotFoundLogLine.Stdout()),
			)),
		workflow.WithDaprdOptions(1, daprd.WithNamespace("other")),
	)

	c.workflow.RegistryN(1).AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1 activity: %w", err)
		}
		return "Processed by app1: " + input, nil
	})

	c.workflow.Registry().AddWorkflowN("CrossNamespaceWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app0: %w", err)
		}

		var output string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithActivityAppID(c.workflow.DaprN(1).AppID())).
			Await(&output)
		if err != nil {
			return fmt.Sprintf("Cross-namespace call failed as expected: %v", err), nil
		}
		return output, nil
	})

	return []framework.Option{
		framework.WithProcesses(c.actorNotFoundLogLine, c.workflow),
	}
}

func (c *crossnamespace) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	client := c.workflow.BackendClient(t, ctx)
	c.workflow.BackendClientN(t, ctx, 1)

	id, err := client.ScheduleNewWorkflow(ctx, "CrossNamespaceWorkflow", api.WithInput("Hello from app0"))
	require.NoError(t, err)

	metadata, err := client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, metadata.GetRuntimeStatus())

	c.actorNotFoundLogLine.EventuallyFoundAll(t)
}
