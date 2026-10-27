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

package noworker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(disconnect))
}

// disconnect asserts that raising an event on a running workflow while its
// worker is disconnected reports that no workflow worker is connected, and
// that the workflow resumes once a worker reconnects.
type disconnect struct {
	workflow *workflow.Workflow
}

func (d *disconnect) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *disconnect) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("bar", time.Hour).Await(nil)
	}))

	worker := d.workflow.ConnectWorker(t, ctx, reg)
	d.workflow.WaitForConnectedWorkers(t, ctx, 1)

	cl := d.workflow.ManagementClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "foo")
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	worker.Disconnect(t)
	d.workflow.WaitForNoConnectedWorkers(t, ctx)

	err = cl.RaiseEvent(ctx, id, "bar")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no workflow worker is connected for app '"+d.workflow.Dapr().AppID()+"'")

	resp, err := d.workflow.GRPCClient(t, ctx).GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
		InstanceId: string(id),
	})
	require.NoError(t, err)
	assert.Equal(t, "RUNNING", resp.GetRuntimeStatus())

	d.workflow.ConnectWorker(t, ctx, reg)
	d.workflow.WaitForConnectedWorkers(t, ctx, 1)

	require.NoError(t, cl.RaiseEvent(ctx, id, "bar"))
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())
}
