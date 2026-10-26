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

package childid

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multigen))
}

type multigen struct {
	workflow *workflow.Workflow
}

func (m *multigen) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *multigen) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	const parentID = "childid-multigen"
	const pinnedID = parentID + "-pinned"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	reg := m.workflow.Registry()

	reg.AddWorkflowN("blocker", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("block").Await(nil)
	})
	reg.AddActivityN("block", func(actx task.ActivityContext) (any, error) {
		inActivity.Store(true)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseCh:
			return nil, nil
		}
	})

	type state struct {
		Gen  int
		Errs []string
	}
	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var s state
		if err := ctx.GetInput(&s); err != nil {
			return nil, err
		}
		if s.Gen == 0 {
			ctx.CallChildWorkflow("blocker", task.WithChildWorkflowInstanceID(pinnedID))
			ctx.WaitForSingleEvent("proceed", time.Minute).Await(nil)
			ctx.ContinueAsNew(state{Gen: 1})
			return nil, nil
		}
		err := ctx.CallChildWorkflow("blocker",
			task.WithChildWorkflowInstanceID(pinnedID),
		).Await(nil)
		if err == nil {
			return nil, nil
		}
		s.Errs = append(s.Errs, err.Error())
		if s.Gen < 2 {
			ctx.ContinueAsNew(state{Gen: s.Gen + 1, Errs: s.Errs})
			return nil, nil
		}
		return s.Errs, nil
	})

	client := m.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID(parentID),
		api.WithInput(state{Gen: 0}),
	)
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	require.NoError(t, client.RaiseEvent(ctx, parentID, "proceed"))

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, parentID)
	require.NoError(t, err, "parent deadlocked: a repeated collision in a later generation was silently deduplicated")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())

	output := meta.GetOutput().GetValue()
	assert.Equal(t, 2, strings.Count(output, "already exists"),
		"generations 1 and 2 must each observe their own collision failure: %s", output)

	ometa, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(pinnedID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING.String(), ometa.GetRuntimeStatus().String())
	assert.Equal(t, "blocker", ometa.GetName())

	close(releaseCh)
	ometa, err = client.WaitForWorkflowCompletion(ctx, pinnedID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String())
}
