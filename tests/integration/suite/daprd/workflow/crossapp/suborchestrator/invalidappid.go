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

package suborchestrator

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
	suite.Register(new(invalidappid))
}

type invalidappid struct {
	workflow             *workflow.Workflow
	actorNotFoundLogLine *logline.LogLine
}

func (i *invalidappid) Setup(t *testing.T) []framework.Option {
	i.actorNotFoundLogLine = logline.New(t,
		logline.WithStdoutLineContains(
			"did not find address for actor",
		),
	)

	i.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(
			exec.WithStdout(i.actorNotFoundLogLine.Stdout()),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(i.actorNotFoundLogLine, i.workflow),
	}
}

func (i *invalidappid) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	i.workflow.Registry().AddOrchestratorN("InvalidAppWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		var result string
		err := ctx.CallSubOrchestrator("ProcessData",
			task.WithSubOrchestratorInput(input),
			task.WithSubOrchestratorAppID("nonexistent-app")).
			Await(&result)
		return fmt.Sprintf("Error handled: %v", err), nil
	})

	client := i.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "InvalidAppWorkflow", api.WithInput("Hello from app0"))
	require.NoError(t, err)

	metadata, err := client.WaitForOrchestrationStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, metadata.RuntimeStatus)

	i.actorNotFoundLogLine.EventuallyFoundAll(t)
}
