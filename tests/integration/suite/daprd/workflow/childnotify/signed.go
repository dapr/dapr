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

package childnotify

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(signed))
}

// signed verifies a notification rebuilt from history carries an attestation
// the signing parent accepts: the re-send is dropped as a duplicate, not
// rejected as tampering, and neither instance is tombstoned.
type signed struct {
	workflow *workflow.Workflow
}

func (s *signed) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t, workflow.WithHistorySigning(t))
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *signed) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const childID = "signed-child"
	reg := s.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("quick", func(*task.WorkflowContext) (any, error) {
		return "signed", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("quick", task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.JSONEq(t, `"signed"`, meta.GetOutput().GetValue())

	s.workflow.StrayFire(t, ctx, 0, childID, true)
	completed, _ := wf.ChildCompletions(t, ctx, cl, api.InstanceID(string(id)), 0)
	assert.Equal(t, 1, completed)
	wf.WaitForRuntimeStatus(t, ctx, cl, id, api.RUNTIME_STATUS_COMPLETED)
	wf.WaitForRuntimeStatus(t, ctx, cl, api.InstanceID(childID), api.RUNTIME_STATUS_COMPLETED)
}
