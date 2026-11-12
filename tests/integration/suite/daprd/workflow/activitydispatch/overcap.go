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

package activitydispatch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

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
	suite.Register(new(overcap))
}

// overcap asserts that, while no sidecar reports slots, more pull-configured
// activities than the per-sidecar cap are delivered the regular way: they
// queue for the engine, run within the cap, and all complete.
type overcap struct {
	workflow *workflow.Workflow
}

func (o *overcap) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullovercap
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: pull
`)),
	)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *overcap) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	const activities = 5
	var running, peak, ran atomic.Int32

	require.NoError(t, o.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		tasks := make([]task.Task, activities)
		for i := range tasks {
			tasks[i] = ctx.CallActivity("act")
		}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	require.NoError(t, o.workflow.Registry().AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		n := running.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(time.Millisecond * 200)
		running.Add(-1)
		ran.Add(1)
		return nil, nil
	}))

	client := o.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "wf")
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, int32(activities), ran.Load())
	assert.Equal(t, int32(1), peak.Load())
}
