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
	suite.Register(new(fanout))
}

type fanout struct {
	workflow *workflow.Workflow
}

func (f *fanout) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fanout) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	const parentID = "occupied-fanout"
	const occupantID = parentID + "-taken"
	const c1ID = parentID + "-c1"
	const c3ID = parentID + "-c3"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	reg := f.workflow.Registry()

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
		t1 := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(c1ID))
		t2 := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(occupantID))
		t3 := ctx.CallChildWorkflow("child", task.WithChildWorkflowInstanceID(c3ID))
		err1 := t1.Await(nil)
		err2 := t2.Await(nil)
		err3 := t3.Await(nil)
		if err1 != nil || err3 != nil {
			return nil, errors.Join(err1, err3)
		}
		if err2 == nil {
			return nil, errors.New("expected occupied child workflow creation to fail")
		}
		return err2.Error(), nil
	})

	client := f.workflow.BackendClient(t, ctx)

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

	for _, id := range []string{c1ID, c3ID} {
		cmeta, cerr := client.FetchWorkflowMetadata(ctx, api.InstanceID(id))
		require.NoError(t, cerr)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), cmeta.GetRuntimeStatus().String())
	}

	ometa, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(occupantID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING.String(), ometa.GetRuntimeStatus().String())
	assert.Equal(t, "occupant", ometa.GetName())

	close(releaseCh)
	ometa, err = client.WaitForWorkflowCompletion(ctx, occupantID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String())
}
