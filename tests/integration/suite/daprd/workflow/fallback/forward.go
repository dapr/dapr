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
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(forward))
}

// forward starts workflows on a fallback host, daprd 1, while it is the only
// daprd of the app hosting the workflow actor types, and then connects a
// worker to daprd 0. The instances placement now gives daprd 0 run at once.
// Those it leaves on daprd 1 for the damping period do not: their start
// reminders fire on daprd 0, the only daprd taking reminders, which forwards
// each to daprd 1, where the turn fails at once for want of a worker and the
// reminder is retried. They run once daprd 1 gives the types up and they
// move to daprd 0, each exactly once.
type forward struct {
	workflow *workflow.Workflow

	lock sync.Mutex
	runs map[api.InstanceID][]time.Time
}

const forwardDamping = 6 * time.Second

func (f *forward) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	f.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithStagedOperations(),
		// Daprd 0 never turns into a fallback host: daprd 1 is the only host
		// until the worker connects.
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, time.Hour)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), daprd.WithWorkflowFallbackSettle(t, time.Millisecond), daprd.WithWorkflowFallbackDamping(t, forwardDamping)),
	)
	f.runs = make(map[api.InstanceID][]time.Time)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *forward) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, f.workflow.RegistryN(0).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		if !ctx.IsReplaying {
			f.lock.Lock()
			f.runs[ctx.ID] = append(f.runs[ctx.ID], time.Now())
			f.lock.Unlock()
		}
		return doneOutput, nil
	}))

	hosts := func(c assert.TestingT, index int) bool {
		return f.workflow.HostsActorType(c, ctx, index, f.workflow.WorkflowActorType(index))
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, hosts(c, 1))
	}, time.Second*10, time.Millisecond*10)
	require.False(t, hosts(t, 0))

	// Enough instances that placement leaves some of them on daprd 1 once
	// daprd 0 hosts the types too.
	const n = 20
	mgmt := f.workflow.ManagementClientN(t, ctx, 1)
	ids := make([]api.InstanceID, n)
	for i := range ids {
		ids[i] = api.InstanceID(fmt.Sprintf("fallback-forward-%d", i))
		octx, cancel := context.WithTimeout(ctx, time.Second*10)
		_, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(ids[i]), api.WithStartTime(time.Now()))
		cancel()
		require.NoError(t, err)
	}

	connected := time.Now()
	f.workflow.ConnectWorkerN(t, ctx, 0, f.workflow.RegistryN(0))
	f.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)

	ran := func() int {
		f.lock.Lock()
		defer f.lock.Unlock()
		return len(f.runs)
	}

	// The instances daprd 0 owns run, while daprd 1 still hosts the types
	// and holds the rest.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, ran())
	}, time.Second*10, time.Millisecond*10)
	early := ran()
	assert.True(t, hosts(t, 1), "daprd 1 gave the workflow actor types up before the damping period")
	assert.Less(t, early, n, "every instance ran while daprd 1, a fallback host, still hosted the workflow actor types")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, hosts(c, 1))
	}, forwardDamping+time.Second*10, time.Millisecond*10)

	fworkflow.WaitForAllCompleted(t, ctx, mgmt, ids...)

	f.lock.Lock()
	defer f.lock.Unlock()
	var late int
	for _, id := range ids {
		if !assert.Len(t, f.runs[id], 1, "runs of %s", id) {
			continue
		}
		// An instance left on daprd 1 runs once daprd 1 gives the types up,
		// a damping period after the worker connected.
		if f.runs[id][0].Sub(connected) >= forwardDamping-time.Second {
			late++
		}
	}
	assert.Positive(t, late, "no instance waited for daprd 1 to give the workflow actor types up")
}
