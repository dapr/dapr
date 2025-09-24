/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package loadbalance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/grpc"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(conflict))
}

type conflict struct {
	cluster []*daprd.Daprd
	single  *daprd.Daprd
}

func (c *conflict) Setup(t *testing.T) []framework.Option {
	place := placement.New(t)
	sched := scheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	clusteredConfig := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
    name: workflowsclustereddeployment
spec:
    features:
    - name: WorkflowsClusteredDeployment
      enabled: true
`
	daprd1 := daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithConfigManifests(t, clusteredConfig),
	)
	daprd2 := daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithAppID(daprd1.AppID()),
		daprd.WithConfigManifests(t, clusteredConfig),
	)
	// run a clustered deployment with two daprds as the same appid
	c.cluster = []*daprd.Daprd{daprd1, daprd2}

	// run a single daprd as a different appid
	c.single = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
	)

	return []framework.Option{
		framework.WithProcesses(place, sched, db, c.cluster[0], c.cluster[1], c.single),
	}
}

func (c *conflict) Run(t *testing.T, ctx context.Context) {
	for _, d := range c.cluster {
		d.WaitUntilRunning(t, ctx)
	}
	c.single.WaitUntilRunning(t, ctx)

	regFactory := func(t *testing.T) *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddOrchestratorN("activity", func(ctx *task.OrchestrationContext) (any, error) {
			require.NoError(t, ctx.CallActivity("abc", task.WithActivityInput("abc")).Await(nil))
			return nil, nil
		}))
		require.NoError(t, r.AddActivityN("abc", func(ctx task.ActivityContext) (any, error) {
			return nil, nil
		}))
		return r
	}

	cluster0BackendClient := client.NewTaskHubGrpcClient(c.cluster[0].GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cluster0BackendClient.StartWorkItemListener(ctx, regFactory(t)))

	singleBackendClient := client.NewTaskHubGrpcClient(c.single.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, singleBackendClient.StartWorkItemListener(ctx, regFactory(t)))

	// verify executor actor is registered in the clustered deployment
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col,
			len(c.cluster[0].GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col,
			len(c.single.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 2)
	}, time.Second*10, time.Millisecond*10)

	clusterClient := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		c.cluster[0].GRPCConn(t, ctx),
		c.cluster[1].GRPCConn(t, ctx),
	), logger.New(t))

	const n = 5
	// create 5 workflows in the clustered deployment
	for i := range n {
		wfID, err := clusterClient.ScheduleNewOrchestration(ctx, "activity", api.WithInstanceID(api.InstanceID(fmt.Sprintf("wf-%d", i))))
		require.NoError(t, err)
		_, err = clusterClient.WaitForOrchestrationCompletion(ctx, wfID)
		require.NoError(t, err)
	}

	// create 5 workflows with the same instance ids in the single daprd
	for i := range n {
		wfID, err := singleBackendClient.ScheduleNewOrchestration(ctx, "activity", api.WithInstanceID(api.InstanceID(fmt.Sprintf("wf-%d", i))))
		require.NoError(t, err)
		_, err = singleBackendClient.WaitForOrchestrationCompletion(ctx, wfID)
		require.NoError(t, err)
	}
}
