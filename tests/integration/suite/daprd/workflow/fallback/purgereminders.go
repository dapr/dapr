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
	"strings"
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
	suite.Register(new(purgereminders))
}

// purgereminders purges, on a fallback host, a completed workflow holding a
// retention reminder and, by force, a workflow waiting on an activity in
// progress and a timer. The purges delete the reminders of the retention and
// activity actor types, which a fallback host hosts too, so the reminders API
// accepts them: none of the workflows' reminders or state is left.
type purgereminders struct {
	workflow *workflow.Workflow
}

func (p *purgereminders) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      completed: "1h"
`)),
	)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purgereminders) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	var inActivity atomic.Bool
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })

	require.NoError(t, p.workflow.Registry().AddWorkflowN("done", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("quick").Await(nil)
	}))
	require.NoError(t, p.workflow.Registry().AddWorkflowN("parked", func(ctx *task.WorkflowContext) (any, error) {
		timer := ctx.CreateTimer(time.Hour)
		if err := ctx.CallActivity("blocked").Await(nil); err != nil {
			return nil, err
		}
		return nil, timer.Await(nil)
	}))
	require.NoError(t, p.workflow.Registry().AddActivityN("quick", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, p.workflow.Registry().AddActivityN("blocked", func(ctx task.ActivityContext) (any, error) {
		inActivity.Store(true)
		select {
		case <-hold:
		case <-ctx.Context().Done():
		}
		return nil, ctx.Context().Err()
	}))

	worker := p.workflow.ConnectWorker(t, ctx, p.workflow.Registry())
	mgmt := p.workflow.ManagementClient(t, ctx)

	doneID, err := mgmt.ScheduleNewWorkflow(ctx, "done", api.WithInstanceID("fallback-purge-retention"))
	require.NoError(t, err)
	_, err = mgmt.WaitForWorkflowCompletion(ctx, doneID)
	require.NoError(t, err)

	parkedID, err := mgmt.ScheduleNewWorkflow(ctx, "parked", api.WithInstanceID("fallback-purge-parked"))
	require.NoError(t, err)
	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	jobs := func(instanceID api.InstanceID) []string {
		var keys []string
		for _, key := range p.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs") {
			if strings.Contains(key, string(instanceID)) {
				keys = append(keys, key)
			}
		}
		return keys
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotEmpty(c, jobs(doneID), "the completed workflow should hold its retention reminder")
		assert.NotEmpty(c, jobs(parkedID), "the parked workflow should hold its timer and activity reminders")
	}, time.Second*10, time.Millisecond*10)

	worker.Disconnect(t)
	p.workflow.WaitForNoConnectedWorkers(t, ctx)

	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	// The daprd turns into a fallback host in place, so the workflow actor
	// type stays hosted; the retry rides out the disconnect callback.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, mgmt.PurgeWorkflowState(octx, doneID))
	}, time.Second*5, time.Millisecond*10)
	require.NoError(t, mgmt.PurgeWorkflowState(octx, parkedID, api.WithForcePurge(true)))

	for _, id := range []api.InstanceID{doneID, parkedID} {
		_, err = mgmt.FetchWorkflowMetadata(octx, id)
		require.ErrorIs(t, err, api.ErrInstanceNotFound, id)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, jobs(id))
		}, time.Second*10, time.Millisecond*10, id)
	}

	db := p.workflow.DB().GetConnection(t)
	var rows int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+p.workflow.DB().TableName()).Scan(&rows))
	assert.Equal(t, 0, rows)
}
