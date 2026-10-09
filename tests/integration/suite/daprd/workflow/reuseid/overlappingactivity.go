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

package reuseid

import (
	"context"
	"sync"
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
	suite.Register(new(overlappingactivity))
}

// overlappingactivity reuses an instance ID while the previous run's activity
// is still executing, so both runs have the same activity task pending on one
// daprd. Both executions must settle and release their activity slots.
type overlappingactivity struct {
	workflow *workflow.Workflow
}

func (o *overlappingactivity) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: overlappingactivity
spec:
  workflow:
    maxConcurrentActivityInvocations: 2
`)),
	)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *overlappingactivity) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	const id = api.InstanceID("reuse-overlappingactivity")

	var slowStarted atomic.Int64
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)

	var probeInside atomic.Int64
	probeRelease := make(chan struct{})
	probeReleaseOnce := sync.OnceFunc(func() { close(probeRelease) })
	t.Cleanup(probeReleaseOnce)

	reg := o.workflow.Registry()
	require.NoError(t, reg.AddActivityN("slow", func(task.ActivityContext) (any, error) {
		slowStarted.Add(1)
		<-release
		return nil, nil
	}))
	require.NoError(t, reg.AddWorkflowN("overlap", func(wctx *task.WorkflowContext) (any, error) {
		wctx.CallActivity("slow")
		return nil, nil
	}))
	require.NoError(t, reg.AddActivityN("probe", func(task.ActivityContext) (any, error) {
		probeInside.Add(1)
		<-probeRelease
		return nil, nil
	}))
	require.NoError(t, reg.AddWorkflowN("probe", func(wctx *task.WorkflowContext) (any, error) {
		a := wctx.CallActivity("probe")
		b := wctx.CallActivity("probe")
		if err := a.Await(nil); err != nil {
			return nil, err
		}
		return nil, b.Await(nil)
	}))

	client := o.workflow.BackendClient(t, ctx)

	for run := int64(1); run <= 2; run++ {
		_, err := client.ScheduleNewWorkflow(ctx, "overlap", api.WithInstanceID(id))
		require.NoError(t, err)
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, run, slowStarted.Load())
		}, time.Second*20, time.Millisecond*10)
	}

	releaseOnce()

	_, err := client.ScheduleNewWorkflow(ctx, "probe", api.WithInstanceID("reuse-overlappingactivity-probe"))
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), probeInside.Load())
	}, time.Second*20, time.Millisecond*10, "both activity slots must be free once the overlapping executions settle")

	probeReleaseOnce()
	meta, err := client.WaitForWorkflowCompletion(ctx, "reuse-overlappingactivity-probe")
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
