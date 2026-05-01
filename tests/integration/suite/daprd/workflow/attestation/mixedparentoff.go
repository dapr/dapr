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

package attestation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(mixedParentOffChildOn))
}

type mixedParentOffChildOn struct {
	workflow *procworkflow.Workflow
}

func (m *mixedParentOffChildOn) Setup(t *testing.T) []framework.Option {
	m.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
		procworkflow.WithSigningDisabledN(0),
	)
	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *mixedParentOffChildOn) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	parentReg := m.workflow.Registry()
	childReg := m.workflow.RegistryN(1)

	parentReg.AddWorkflowN("attest-mixed-parent-off", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("remote-noop",
			task.WithActivityAppID(m.workflow.DaprN(1).AppID()),
		).Await(nil)
	})

	childReg.AddActivityN("remote-noop", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})

	parentClient := m.workflow.BackendClient(t, ctx)
	m.workflow.BackendClientN(t, ctx, 1)

	id, err := parentClient.ScheduleNewWorkflow(ctx, "attest-mixed-parent-off")
	require.NoError(t, err)

	meta, err := parentClient.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))

	assert.Equal(t, 0, fworkflow.ExtSigCertCount(t, ctx, m.workflow.DB(), string(id)))
}
