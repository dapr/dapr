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

package fallback

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(placementdown))
}

// placementdown stops the placement service while one daprd of an app has a
// worker and the other, without one, has given the workflow actor types up
// after the damping period. Losing the placement tables tells the daprd
// without a worker nothing about the other daprd's worker: it must not read
// the outage as no daprd of the app having one, and does not take the types
// back as a fallback host, neither while placement is down nor once it is
// back. Workflows started through either daprd then still run on the daprd
// with the worker.
//
// Once placement is back, the first table a daprd receives lists only the
// daprds which reconnected so far. When the daprd without a worker
// reconnects first, it sees no executor in that table, and waits the settle
// period before it hosts the types; the daprd with the worker reconnects
// well within it, which cancels it.
type placementdown struct {
	workflow  *workflow.Workflow
	placeBack *placement.Placement
	// logB captures the log of the daprd without a worker.
	logB *logline.LogLine
}

// placementdownSettle is the default settle period, several times the skew
// between the daprds' reconnects to placement.
const placementdownSettle = 3 * time.Second

func (p *placementdown) Setup(t *testing.T) []framework.Option {
	p.logB = logline.New(t, logline.WithCaptureAll())
	p.workflow = newPair(t, 2*time.Second, placementdownSettle,
		workflow.WithPlacementService(),
		workflow.WithDaprdOptions(1, daprd.WithExecOptions(
			exec.WithStdout(p.logB.Stdout()),
			exec.WithStderr(p.logB.Stderr()),
		)),
	)
	p.placeBack = placementBack(t, p.workflow)

	return []framework.Option{
		framework.WithProcesses(p.logB, p.workflow),
	}
}

func (p *placementdown) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, p.workflow.RegistryN(0).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return in, nil
	}))

	p.workflow.ConnectWorkerN(t, ctx, 0, p.workflow.RegistryN(0))
	p.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, p.workflow.HostsActorType(c, ctx, 1, p.workflow.WorkflowActorType(1)))
		assert.True(c, p.workflow.HostsActorType(c, ctx, 0, p.workflow.WorkflowActorType(0)))
	}, time.Second*20, time.Millisecond*50)

	// hostedB reports whether the daprd without a worker hosts the workflow
	// actor type; a failed metadata read reports it does not.
	hostedB := func() bool {
		var rerr workflow.ReadErrors
		return p.workflow.HostsActorType(&rerr, ctx, 1, p.workflow.WorkflowActorType(1)) && !rerr.Failed
	}

	// The daprd without a worker learns of the other's executor only once
	// the dissemination round which lists it completes there. Stopped before
	// that, placement leaves it seeing no executor anywhere, and it rightly
	// hosts the types once its settle period ends.
	executorType := p.workflow.ExecutorActorType(0)
	require.Eventually(t, func() bool {
		for line := range bytes.Lines(p.logB.StdoutBuffer()) {
			if bytes.Contains(line, []byte("Dissemination complete")) && bytes.Contains(line, []byte(executorType)) {
				return true
			}
		}
		return false
	}, time.Second*20, time.Millisecond*50)

	p.workflow.Placement().Cleanup(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, "placement: disconnected", placementStatus(c, ctx, p.workflow, 1))
	}, time.Second*20, time.Millisecond*50)

	// Several damping periods long.
	assert.Never(t, hostedB, time.Second*10, time.Millisecond*50,
		"the daprd without a worker took the workflow actor types while placement was down")

	runPlacementBack(t, ctx, p.placeBack)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, "placement: connected", placementStatus(c, ctx, p.workflow, 0))
		assert.Equal(c, "placement: connected", placementStatus(c, ctx, p.workflow, 1))
	}, time.Second*20, time.Millisecond*50)
	assert.Never(t, hostedB, time.Second*5, time.Millisecond*50,
		"the daprd without a worker took the workflow actor types once placement was back")

	mgmt := []*client.TaskHubGrpcClient{
		p.workflow.ManagementClientN(t, ctx, 0),
		p.workflow.ManagementClientN(t, ctx, 1),
	}

	const instances = 10
	ids := make([]api.InstanceID, 0, instances)
	for i := range instances {
		m := mgmt[i%2]
		var id api.InstanceID
		// The first calls ride out the daprds settling on the new tables.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			sctx, cancel := context.WithTimeout(ctx, time.Second*5)
			defer cancel()
			var err error
			id, err = m.ScheduleNewWorkflow(sctx, "wf",
				api.WithInstanceID(api.InstanceID(fmt.Sprintf("fallback-placementdown-%d", i))),
				api.WithInput(fmt.Sprintf("in-%d", i)),
			)
			assert.NoError(c, err)
		}, time.Second*20, time.Millisecond*100)
		ids = append(ids, id)
	}

	for i, id := range ids {
		wctx, cancel := context.WithTimeout(ctx, time.Second*10)
		meta := fworkflow.WaitForCompleted(t, wctx, mgmt[(i+1)%2], id)
		cancel()
		assert.Equal(t, fmt.Sprintf(`"in-%d"`, i), meta.GetOutput().GetValue(), id)
	}

	// Every instance ran on the daprd with the worker: the other served
	// nothing as a fallback host.
	assert.False(t, p.workflow.HostsActorType(t, ctx, 1, p.workflow.WorkflowActorType(1)))
	assert.Zero(t, stagedStarts(t, ctx, p.workflow, 1))
}
