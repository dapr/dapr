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

package accesspolicy

import (
	"context"
	"fmt"
	"testing"

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

// crossnamespace tests that cross-namespace workflow calls are blocked.
// This uses standalone mode (no mTLS) to test placement-level namespace
// isolation. The workflow_acl.go checkNamespace function provides additional
// defense-in-depth when mTLS is enabled (tested via unit tests).
type crossnamespace struct {
	wf                   *workflow.Workflow
	actorNotFoundLogLine *logline.LogLine
}

func (c *crossnamespace) Setup(t *testing.T) []framework.Option {
	c.actorNotFoundLogLine = logline.New(t,
		logline.WithStdoutLineContains(
			"did not find address for actor",
		),
	)

	c.wf = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0,
			daprd.WithNamespace("default"),
			daprd.WithConfigManifests(t, configWithFeatureFlag()),
			daprd.WithExecOptions(
				exec.WithStdout(c.actorNotFoundLogLine.Stdout()),
			),
		),
		workflow.WithDaprdOptions(1,
			daprd.WithNamespace("other-ns"),
			daprd.WithConfigManifests(t, configWithFeatureFlag()),
		),
	)

	c.wf.Registry().AddWorkflowN("CrossNsWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("TargetWF",
			task.WithChildWorkflowAppID(c.wf.DaprN(1).AppID())).
			Await(&output)
		if err != nil {
			return fmt.Sprintf("cross-ns call blocked: %v", err), nil
		}
		return output, nil
	})

	c.wf.RegistryN(1).AddWorkflowN("TargetWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})

	return []framework.Option{
		framework.WithProcesses(c.actorNotFoundLogLine, c.wf),
	}
}

func (c *crossnamespace) Run(t *testing.T, ctx context.Context) {
	c.wf.WaitUntilRunning(t, ctx)

	client0 := c.wf.BackendClient(t, ctx)
	c.wf.BackendClientN(t, ctx, 1)

	t.Run("cross-namespace workflow call is blocked by placement isolation", func(t *testing.T) {
		_, err := client0.ScheduleNewWorkflow(ctx, "CrossNsWorkflow", api.WithInput("test"))
		require.NoError(t, err)

		c.actorNotFoundLogLine.EventuallyFoundAll(t)
	})
}
