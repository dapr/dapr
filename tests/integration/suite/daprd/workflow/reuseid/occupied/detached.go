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
	suite.Register(new(detached))
}

type detached struct {
	workflow *workflow.Workflow
}

func (d *detached) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *detached) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	const spawnerID = "occupied-detached"
	const occupantID = spawnerID + "-taken"

	var inActivity atomic.Bool
	var childRan atomic.Bool
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	reg := d.workflow.Registry()

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
		childRan.Store(true)
		return nil, nil
	})
	reg.AddWorkflowN("spawner", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("child",
			task.WithDetachedWorkflowInstanceID(occupantID),
		)
		return nil, err
	})

	client := d.workflow.BackendClient(t, ctx)

	_, err := client.ScheduleNewWorkflow(ctx, "occupant", api.WithInstanceID(occupantID))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	_, err = client.ScheduleNewWorkflow(ctx, "spawner", api.WithInstanceID(spawnerID))
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, spawnerID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())

	ometa, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(occupantID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING.String(), ometa.GetRuntimeStatus().String())
	assert.Equal(t, "occupant", ometa.GetName())

	close(releaseCh)
	ometa, err = client.WaitForWorkflowCompletion(ctx, occupantID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String())
	assert.Equal(t, "occupant", ometa.GetName())
	assert.False(t, childRan.Load())
}
