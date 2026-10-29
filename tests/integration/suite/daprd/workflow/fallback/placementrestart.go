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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(placementrestart))
}

// placementrestart stops the placement service under a fallback host and
// starts it again on the same address. The fallback host keeps the last
// answer of its placement tables, that no daprd of its app has an executor,
// so it hosts the workflow actor types throughout: while placement is down,
// and once it is back, with no settle period in between. Operations through
// it work once placement is back, and the instances run once a worker
// connects.
type placementrestart struct {
	workflow  *workflow.Workflow
	placeBack *placement.Placement
}

func (p *placementrestart) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		workflow.WithPlacementService(),
		withSettle(t, 0),
	)

	place := p.workflow.Placement()
	opts := []placement.Option{
		placement.WithID(place.ID()),
		placement.WithPort(place.Port()),
		placement.WithInitialCluster(place.InitialCluster()),
		placement.WithInitialClusterPorts(place.InitialClusterPorts()...),
	}
	if sen := p.workflow.Sentry(); sen != nil {
		opts = append(opts, placement.WithSentry(t, sen))
	}
	p.placeBack = placement.New(t, opts...)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *placementrestart) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, p.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return doneOutput, nil
	}))

	workflowType := p.workflow.WorkflowActorType(0)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, p.workflow.HostsActorType(c, ctx, 0, workflowType))
	}, time.Second*10, time.Millisecond*10)

	// A start time skips the wait for the instance to start, which a
	// fallback host still makes and no worker would end.
	mgmt := p.workflow.ManagementClient(t, ctx)
	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	beforeID, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("fallback-placementrestart-before"), api.WithStartTime(time.Now()))
	require.NoError(t, err)

	// Watch the fallback host throughout the restart.
	unhosted := p.workflow.WatchActorTypeHosted(t, ctx, 0, workflowType)

	placementIs := func(state string) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			rt := p.workflow.Dapr().GetMetaActorRuntime(c, ctx)
			if assert.NotNil(c, rt) {
				assert.Equal(c, "placement: "+state, rt.Placement)
			}
		}, time.Second*20, time.Millisecond*10)
	}

	p.workflow.Placement().Cleanup(t)
	placementIs("disconnected")

	p.placeBack.Run(t, ctx)
	t.Cleanup(func() { p.placeBack.Cleanup(t) })
	p.placeBack.WaitUntilRunning(t, ctx)
	placementIs("connected")

	// The daprd reports its types to the new placement service, the
	// fallback host's workflow actor types among them.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		table := p.placeBack.PlacementTables(t, ctx).Tables["default"]
		if !assert.NotNil(c, table) || !assert.Len(c, table.Hosts, 1) {
			return
		}
		assert.Contains(c, table.Hosts[0].Entities, workflowType)
	}, time.Second*20, time.Millisecond*10)

	// Operations through the fallback host work again.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cctx, ccancel := context.WithTimeout(ctx, time.Second*3)
		defer ccancel()
		meta, merr := mgmt.FetchWorkflowMetadata(cctx, beforeID)
		if assert.NoError(c, merr) {
			assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())
		}
	}, time.Second*10, time.Millisecond*10)
	sctx, scancel := context.WithTimeout(ctx, time.Second*10)
	defer scancel()
	afterID, err := mgmt.ScheduleNewWorkflow(sctx, "wf", api.WithInstanceID("fallback-placementrestart-after"), api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.False(t, unhosted(), "the fallback host gave the workflow actor types up across the placement restart")

	p.workflow.ConnectWorker(t, ctx, p.workflow.Registry())
	fworkflow.WaitForAllCompleted(t, ctx, mgmt, beforeID, afterID)
}
