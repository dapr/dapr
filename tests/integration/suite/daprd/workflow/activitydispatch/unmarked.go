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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(unmarked))
}

// unmarked asserts a delivery without WatchJobsResponse.pull is not a pull
// job on the sidecar, even though the job carries JobMetadata.pull, which is
// how a scheduler without pull dispatch delivers it. The proxy strips the
// mark, so the sidecar runs the activity like any reminder: it does not keep
// it across a stream drop, and the scheduler delivers it again once its
// reattach grace ends instead of finding it reattached.
type unmarked struct {
	sched    *scheduler.Scheduler
	proxy    *proxy.Proxy
	workflow *workflow.Workflow
	stripped atomic.Int64
}

const unmarkedAppID = "pull-unmarked"

func (u *unmarked) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullunmarked
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: pull
`
	u.sched = scheduler.New(t)
	u.proxy = proxy.New(t, u.sched, proxy.WithWatchJobsResponseFn(func(resp *schedulerv1pb.WatchJobsResponse) {
		if resp.GetPull() {
			u.stripped.Add(1)
			resp.Pull = nil
		}
	}))
	u.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(u.sched),
		workflow.WithSchedulerAddress(u.proxy.Address()),
		workflow.WithSigning(false),
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(unmarkedAppID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(unmarkedAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(u.sched, u.proxy, u.workflow),
	}
}

func (u *unmarked) Run(t *testing.T, ctx context.Context) {
	u.workflow.WaitUntilRunning(t, ctx)

	var started [2]atomic.Int64
	var release [2]chan struct{}
	for i := range 2 {
		release[i] = make(chan struct{})
		u.workflow.RegistryN(i).AddWorkflowN("single", func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity("slow").Await(nil)
		})
		u.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			started[i].Add(1)
			<-release[i]
			return nil, nil
		})
	}

	clients := []*client.TaskHubGrpcClient{
		u.workflow.BackendClientN(t, ctx, 0),
		u.workflow.BackendClientN(t, ctx, 1),
	}
	id, err := clients[0].ScheduleNewWorkflow(ctx, "single", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), started[0].Load()+started[1].Load())
	}, time.Second*20, time.Millisecond*10)
	require.Equal(t, int64(1), u.stripped.Load(), "the scheduler marked the delivery and the proxy stripped it")

	u.proxy.DropWatchJobs()

	// Not a pull job on the sidecar, so the reconnecting stream does not
	// list it as running and the scheduler delivers it again when its
	// reattach grace ends, which the proxy sees as a second marked delivery.
	// Compare reattach, where the mark keeps the activity and nothing is
	// delivered again.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), u.stripped.Load(), "an unmarked delivery must not be kept as a pull job")
	}, time.Second*60, time.Millisecond*10)

	for i := range 2 {
		close(release[i])
	}
	_, err = clients[0].WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
