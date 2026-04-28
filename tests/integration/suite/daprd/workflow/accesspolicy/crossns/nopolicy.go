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

package crossns

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	crossnswf "github.com/dapr/dapr/tests/integration/framework/process/workflow/crossns"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(nopolicy))
}

// nopolicy asserts that cross-namespace invocation is denied when the
// WorkflowCrossNamespace feature is enabled but no WorkflowAccessPolicy
// exists on the target. Cross-namespace is a security boundary that
// should not be implicitly open: the target handler requires an explicit
// ingress rule. This is stricter than the same-namespace default (which
// allows nil policies through), and the caller sees the denial as a
// terminal ChildWorkflowInstanceFailed("WorkflowAccessPolicyDenied")
// event.
type nopolicy struct {
	fx *crossnswf.Workflow
}

func (c *nopolicy) Setup(t *testing.T) []framework.Option {
	c.fx = crossnswf.New(t,
		crossnswf.WithCaller("xns-nopol-caller", "default"),
		crossnswf.WithTarget("xns-nopol-target", "other-ns"),
		crossnswf.WithWorkflowAccessPolicyEnabled(true),
	)
	return []framework.Option{framework.WithProcesses(c.fx)}
}

func (c *nopolicy) Run(t *testing.T, ctx context.Context) {
	c.fx.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		if err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(c.fx.Target().AppID()),
			task.WithChildWorkflowAppNamespace("other-ns")).
			Await(&output); err != nil {
			return err.Error(), nil //nolint:nilerr
		}
		return output, nil
	}))

	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		return crossnswf.ShouldNotRun, nil
	}))

	callerClient := c.fx.StartListeners(t, ctx, callerReg, targetReg)

	id, err := callerClient.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Contains(t, metadata.GetOutput().GetValue(), "WorkflowAccessPolicyDenied")
}
