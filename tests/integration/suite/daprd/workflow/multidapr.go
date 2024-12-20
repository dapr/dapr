/*
Copyright 2023 The Dapr Authors
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

package workflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multidapr))
}

type multidapr struct {
	daprds []*daprd.Daprd
}

func (m *multidapr) Setup(t *testing.T) []framework.Option {
	place := placement.New(t)
	sched := scheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	m.daprds = make([]*daprd.Daprd, 3)
	m.daprds[0] = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
	)
	m.daprds[1] = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithAppID(m.daprds[0].AppID()),
	)
	m.daprds[2] = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithAppID(m.daprds[0].AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(place, sched, db, m.daprds[0], m.daprds[1], m.daprds[2]),
	}
}

func (m *multidapr) Run(t *testing.T, ctx context.Context) {
	m.daprds[0].WaitUntilRunning(t, ctx)
	m.daprds[1].WaitUntilRunning(t, ctx)
	m.daprds[2].WaitUntilRunning(t, ctx)

	d := m.daprds[0] // use the first Dapr instance to call

	backendClient := client.NewTaskHubGrpcClient(d.GRPCConn(t, ctx), backend.DefaultLogger())

	t.Run("schedule_workflow_with_multidaprple_daprd_instances", func(t *testing.T) {
		r := task.NewTaskRegistry()
		r.AddOrchestratorN("ScheduleWorkflowWithmultidaprpleDaprdInstances", func(ctx *task.OrchestrationContext) (any, error) {
			var input string
			if err := ctx.GetInput(&input); err != nil {
				return nil, err
			}
			var output string
			err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
			return output, err
		})
		r.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
			var name string
			if err := ctx.GetInput(&name); err != nil {
				return nil, err
			}
			return fmt.Sprintf("Hello, %s!", name), nil
		})

		taskhubCtx, cancelTaskhub := context.WithCancel(ctx)
		require.NoError(t, backendClient.StartWorkItemListener(taskhubCtx, r))
		defer cancelTaskhub()

		id, err := backendClient.ScheduleNewOrchestration(ctx, "ScheduleWorkflowWithmultidaprpleDaprdInstances", api.WithInstanceID("Dapr"), api.WithInput("Dapr"))
		require.NoError(t, err)

		metadata, err := backendClient.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
		assert.Equal(t, `"Hello, Dapr!"`, metadata.GetOutput().GetValue())
	})
}
