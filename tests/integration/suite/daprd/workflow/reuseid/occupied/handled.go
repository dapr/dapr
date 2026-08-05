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

package occupied

import (
	"context"
	"errors"
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
	suite.Register(new(handled))
}

type handled struct {
	workflow *workflow.Workflow
}

func (h *handled) Setup(t *testing.T) []framework.Option {
	h.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *handled) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	const parentID = "occupied-handled"
	const occupantID = parentID + "-taken"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	reg := h.workflow.Registry()

	reg.AddWorkflowN("occupant", func(ctx *task.WorkflowContext) (any, error) {
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
	reg.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		err := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowInstanceID(occupantID),
		).Await(nil)
		if err == nil {
			return nil, errors.New("expected child workflow creation to fail")
		}
		return err.Error(), nil
	})

	client := h.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "occupant", api.WithInstanceID(occupantID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	_, err = client.ScheduleNewWorkflow(ctx, "parent", api.WithInstanceID(parentID))
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())
	assert.Contains(t, meta.GetOutput().GetValue(), "already exists")

	ometa, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(occupantID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING.String(), ometa.GetRuntimeStatus().String())
	assert.Equal(t, "occupant", ometa.GetName())

	close(releaseCh)
	ometa, err = client.WaitForWorkflowCompletion(ctx, occupantID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String())
}
