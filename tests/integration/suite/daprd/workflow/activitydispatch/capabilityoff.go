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

// capabilityoff asserts what a sidecar with pull configured does when its
// scheduler stops advertising pull support, as an older scheduler joining
// would: it says so in its log, drops the result of the pull activity it was
// running and logs that, and runs later activities by regular dispatch.
type capabilityoff struct {
	sched    *scheduler.Scheduler
	proxy    *proxy.Proxy
	logline  *logline.LogLine
	workflow *workflow.Workflow

	off atomic.Bool
}

const capabilityoffAppID = "pull-capabilityoff"

func (c *capabilityoff) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullcapabilityoff
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: pull
`
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
			daprd.WithConfigManifests(t, configManifest),
			daprd.WithAppID(capabilityoffAppID),
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

	var started atomic.Int64
	releaseCh := make(chan struct{})
	c.workflow.Registry().AddWorkflowN("single", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("slow").Await(nil)
	})
	c.workflow.Registry().AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
		if started.Add(1) == 1 {
			<-releaseCh
		}
		return nil, nil
	})

	client := c.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "single", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Equal(col, int64(1), started.Load())
	}, time.Second*20, time.Millisecond*10)
	c.sched.WaitUntilPullInflight(t, ctx, 1, "app_id:"+capabilityoffAppID)
	assert.False(t, c.logline.Contains("Pull activity dispatch is configured but not every scheduler"))

	c.off.Store(true)
	c.proxy.DropWatchHosts()
	c.logline.EventuallyContains(t, "Pull activity dispatch is configured but not every scheduler", time.Second*20, time.Millisecond*10)

	close(releaseCh)
	c.logline.EventuallyContains(t, "Dropping the result of pull reminder", time.Second*20, time.Millisecond*10)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// With pull off the sidecar reports no slots, so the scheduler routes the
	// next activity as a regular reminder.
	id, err = client.ScheduleNewWorkflow(ctx, "single", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
