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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reminders))
}

// reminders starts workflows with an activity and a timer through a fallback
// host while another daprd of the app has a worker. The damping period is
// long, so the fallback host keeps the workflow actor types, and its share of
// the instances, while the reminders of every instance come due. It does not
// advertise the types to the scheduler, so the scheduler delivers none of
// those reminders to it: each lands on the daprd with the worker, which runs
// the instances it owns and forwards the others to the fallback host, where
// the turn fails until the damping period ends and they move. Every instance
// completes, and the fallback host never receives a reminder.
//
// The fallback host counts every actor reminder its scheduler stream
// delivers, in dapr_runtime_actor_reminders_fired_total, before routing it.
// Had it advertised the types, the scheduler would deliver to it the
// reminders of the instances it owns, or a share of all of them, and the
// count would be above zero.
type reminders struct {
	workflow *workflow.Workflow
}

const remindersDamping = 25 * time.Second

func (r *reminders) Setup(t *testing.T) []framework.Option {
	r.workflow = newPair(t, remindersDamping, fallbackSettle)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reminders) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.RegistryN(0).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		var out string
		if err := ctx.CallActivity("act", task.WithActivityInput(in)).Await(&out); err != nil {
			return nil, err
		}
		if err := ctx.CreateTimer(time.Second).Await(nil); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, r.workflow.RegistryN(0).AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return "done-" + in, nil
	}))

	// Both daprds are fallback hosts until the worker connects.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, r.workflow.HostsActorType(c, ctx, 1, r.workflow.WorkflowActorType(1)))
	}, time.Second*10, time.Millisecond*50)

	r.workflow.ConnectWorkerN(t, ctx, 0, r.workflow.RegistryN(0))
	r.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	connected := time.Now()

	mgmt := r.workflow.ManagementClientN(t, ctx, 1)

	const instances = 30
	ids := make([]api.InstanceID, 0, instances)
	for i := range instances {
		sctx, cancel := context.WithTimeout(ctx, time.Second*10)
		id, err := mgmt.ScheduleNewWorkflow(sctx, "wf",
			api.WithInstanceID(api.InstanceID(fmt.Sprintf("fallback-reminders-%d", i))),
			api.WithInput(strconv.Itoa(i)),
		)
		cancel()
		require.NoError(t, err)
		ids = append(ids, id)
	}

	completed := func(c assert.TestingT) int {
		var n int
		for _, id := range ids {
			fctx, cancel := context.WithTimeout(ctx, time.Second*3)
			meta, err := mgmt.FetchWorkflowMetadata(fctx, id)
			cancel()
			if assert.NoError(c, err, id) && meta.GetRuntimeStatus() == protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED {
				n++
			}
		}
		return n
	}

	// The instances the daprd with the worker owns run through their
	// activity and timer reminders, while the fallback host keeps its share:
	// their start reminders come due and are retried meanwhile.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		n := completed(c)
		assert.Positive(c, n)
		assert.Less(c, n, instances)
		owned, hosted := r.workflow.DaprN(1).ActiveActorCount(c, ctx, r.workflow.WorkflowActorType(1))
		assert.True(c, hosted)
		assert.Positive(c, owned, "the fallback host should own a share of the instances")
	}, time.Second*15, time.Millisecond*100)
	// The reminders of the instances the fallback host owns are delivered to
	// the daprd with the worker, which forwards them to the fallback host,
	// where the turn fails: they are retried, a few times over.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		failed := r.workflow.DaprN(0).Metrics(c, ctx).SumWithLabels("dapr_runtime_actor_reminders_fired_total",
			"actor_type:"+r.workflow.WorkflowActorType(0), "success:false")
		assert.GreaterOrEqual(c, failed, 5.0)
	}, time.Second*15, time.Millisecond*100)
	require.Less(t, time.Since(connected), remindersDamping-time.Second*3,
		"the fallback host must still hold the workflow actor types")
	assert.True(t, r.workflow.HostsActorType(t, ctx, 1, r.workflow.WorkflowActorType(1)))
	assert.Zero(t, reminderDeliveries(t, ctx, r.workflow, 1), "the scheduler delivered a reminder to the fallback host")
	assert.Positive(t, reminderDeliveries(t, ctx, r.workflow, 0))

	// Once the damping period ends, the fallback host gives its instances to
	// the daprd with the worker, where they complete.
	for i, id := range ids {
		wctx, cancel := context.WithTimeout(ctx, remindersDamping+time.Second*20)
		meta := fworkflow.WaitForCompleted(t, wctx, mgmt, id)
		cancel()
		assert.Equal(t, fmt.Sprintf(`"done-%d"`, i), meta.GetOutput().GetValue(), id)
	}
	assert.False(t, r.workflow.HostsActorType(t, ctx, 1, r.workflow.WorkflowActorType(1)))
	assert.Zero(t, reminderDeliveries(t, ctx, r.workflow, 1), "the scheduler delivered a reminder to the daprd without a worker")
}
