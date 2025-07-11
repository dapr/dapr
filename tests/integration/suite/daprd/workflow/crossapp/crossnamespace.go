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

package crossapp

import (
	"context"
	"fmt"
	"testing"
	"time"

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

// crossnamespace tests that calling activities across different namespaces fails
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

	// App1: Activity in different namespace
	c.workflow.RegistryN(1).AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1 activity: %w", err)
		}
		return "Processed by app1: " + input, nil
	})

	// App0: Orchestrator that tries to call app2 in different namespace
	c.workflow.Registry().AddOrchestratorN("CrossNamespaceWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app0: %w", err)
		}

		// Try to call activity on app1 in different namespace - this should fail
		var output string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(c.workflow.DaprN(1).AppID())).
			Await(&output)
		if err != nil {
			// Expected to fail due to namespace isolation
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

	// Start workflow listener for apps
	client0 := c.workflow.BackendClient(t, ctx)
	c.workflow.BackendClientN(t, ctx, 1)

	// Expect completion to hang, so timeout
	// dapr will log about 'did not find address for actor'
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()

	// Start workflow from app0 (default namespace)
	_, err := client0.ScheduleNewOrchestration(waitCtx, "CrossNamespaceWorkflow", api.WithInput("Hello from app0"))
	require.Error(t, err)
	assert.EqualError(t, err, "context deadline exceeded")
	c.actorNotFoundLogLine.EventuallyFoundAll(t)
}
