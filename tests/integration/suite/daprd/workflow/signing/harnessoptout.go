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

package signing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(harnessoptout))
}

type harnessoptout struct {
	workflow *workflow.Workflow
	joiner   *daprd.Daprd
}

func (h *harnessoptout) Setup(t *testing.T) []framework.Option {
	h.workflow = workflow.New(t,
		workflow.WithMTLS(t),
		workflow.WithSigning(false),
	)
	h.joiner = daprd.New(t, append([]daprd.Option{
		daprd.WithAppID(h.workflow.Dapr().AppID()),
		daprd.WithResourceFiles(h.workflow.DB().GetComponent(t)),
		daprd.WithPlacementAddresses(h.workflow.Placement().Address()),
		daprd.WithSchedulerAddresses(h.workflow.Scheduler().Address()),
	}, h.workflow.JoinOptions(t)...)...)
	return []framework.Option{
		framework.WithProcesses(h.workflow, h.joiner),
	}
}

func (h *harnessoptout) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)
	h.joiner.WaitUntilRunning(t, ctx)

	assert.False(t, h.workflow.Signing())
	assert.NotNil(t, h.workflow.Sentry(), "opting out of signing must keep the requested mTLS")
	for _, d := range []*daprd.Daprd{h.workflow.Dapr(), h.joiner} {
		assert.NotContains(t, d.GetMetaEnabledFeatures(t, ctx), "WorkflowHistorySigning")
	}

	require.NoError(t, h.workflow.Registry().AddWorkflowN("wf", func(*task.WorkflowContext) (any, error) {
		return "ok", nil
	}))
	cl := h.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "wf")
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
