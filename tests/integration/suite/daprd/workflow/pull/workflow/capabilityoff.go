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

package workflow

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(capabilityoff))
}

// capabilityoff checks that when the scheduler stops advertising pull support
// the sidecar logs it and runs later workflow turns by regular dispatch.
type capabilityoff struct {
	sched    *scheduler.Scheduler
	proxy    *proxy.Proxy
	logline  *logline.LogLine
	workflow *workflow.Workflow

	off atomic.Bool
}

const capabilityoffAppID = "workflow-capabilityoff"

func (c *capabilityoff) Setup(t *testing.T) []framework.Option {
	c.sched = scheduler.New(t)
	c.proxy = proxy.New(t, c.sched, proxy.WithWatchHostsResponseFn(func(resp *schedulerv1pb.WatchHostsResponse) {
		if c.off.Load() {
			for _, host := range resp.GetHosts() {
				host.SupportsPullSlots = false
			}
		}
	}))
	c.logline = logline.New(t, logline.WithCaptureAll())
	c.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(c.sched),
		workflow.WithSchedulerAddress(c.proxy.Address()),
		workflow.WithSigning(false),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID(capabilityoffAppID),
			daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowcapabilityoff
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`),
			daprd.WithExecOptions(
				exec.WithStdout(c.logline.Stdout()),
				exec.WithStderr(c.logline.Stderr()),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(c.logline, c.sched, c.proxy, c.workflow),
	}
}

func (c *capabilityoff) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const logLine = "Pull dispatch is configured but not every scheduler"

	var seen sync.Map
	held := make(chan api.InstanceID, 2)
	release := make(chan struct{})
	done := ctx.Done()
	require.NoError(t, c.workflow.Registry().AddWorkflowN("hold", func(ctx *task.WorkflowContext) (any, error) {
		// The start returns once the first workflow turn commits, so hold blocks in its second.
		if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
			return nil, err
		}
		if !ctx.IsReplaying {
			// A redelivered workflow turn runs this again, so each instance blocks once.
			if _, loaded := seen.LoadOrStore(ctx.ID, true); !loaded {
				held <- ctx.ID
				select {
				case <-release:
				case <-done:
				}
			}
		}
		return nil, nil
	}))

	waitHeld := func(want api.InstanceID) {
		t.Helper()
		select {
		case id := <-held:
			require.Equal(t, want, id)
		case <-time.After(time.Second * 10):
			require.Fail(t, "workflow turn not held", string(want))
		}
	}
	unhold := func() {
		t.Helper()
		select {
		case release <- struct{}{}:
		case <-time.After(time.Second * 10):
			require.Fail(t, "no held workflow turn to release")
		}
	}
	inflight := func() float64 {
		return c.sched.Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_inflight", "app_id:"+capabilityoffAppID)
	}

	client := c.workflow.BackendClient(t, ctx)

	first, err := client.ScheduleNewWorkflow(ctx, "hold")
	require.NoError(t, err)
	waitHeld(first)
	c.sched.WaitUntilWorkflowPullInflight(t, ctx, 1, "app_id:"+capabilityoffAppID)
	assert.False(t, c.logline.Contains(logLine))

	c.off.Store(true)
	c.proxy.DropWatchHosts()
	c.logline.EventuallyContains(t, logLine, time.Second*10, time.Millisecond*10)

	unhold()
	c.logline.EventuallyContains(t, "Dropping the result of pull reminder", time.Second*10, time.Millisecond*10)
	_, err = client.WaitForWorkflowCompletion(ctx, first)
	require.NoError(t, err)

	// With pull off the sidecar reports no slots, so the workflow turns of this
	// workflow are regular reminders. Only the dropped workflow turn of first can still
	// be counted in flight, until the scheduler delivers it again.
	second, err := client.ScheduleNewWorkflow(ctx, "hold")
	require.NoError(t, err)
	waitHeld(second)
	assert.LessOrEqual(t, inflight(), 1.0)
	unhold()
	meta, err := client.WaitForWorkflowCompletion(ctx, second)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
}
