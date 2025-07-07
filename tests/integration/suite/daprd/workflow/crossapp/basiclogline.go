/*
Copyright 2025 The Dapr Authors
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

package crossapp

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
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
	suite.Register(new(basiclogline))
}

// basiclogline demonstrates calling activities across different Dapr applications.
type basiclogline struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry
}

func (b *basiclogline) Setup(t *testing.T) []framework.Option {
	b.place = placement.New(t)
	b.sched = scheduler.New(t,
		scheduler.WithLogLevel("debug"))
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	app1 := app.New(t)
	app2 := app.New(t)

	// Create registries for each app and register orchestrator/activity
	b.registry1 = task.NewTaskRegistry()
	b.registry2 = task.NewTaskRegistry()

	appID1 := uuid.New().String()
	appID2 := uuid.New().String()

	b.registry2.AddActivityN("ProcessData", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app2 activity: %w", err)
		}
		return fmt.Sprintf("Processed by app2: %s", input), nil
	})

	b.registry1.AddOrchestratorN("CrossAppWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in app1: %w", err)
		}

		var output string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID(appID2)).
			Await(&output)
		if err != nil {
			return nil, fmt.Errorf("failed to execute activity in app2: %w", err)
		}
		return output, nil
	})

	wfOrchestrationLog := logline.New(t,
		logline.WithStdoutLineContains(
			fmt.Sprintf("invoking execute method on activity actor 'dapr.internal.default.%s.activity||", appID2),
			"workflow completed with status 'ORCHESTRATION_STATUS_COMPLETED' workflowName 'CrossAppWorkflow'",
		),
	)

	wfActivityLog := logline.New(t,
		logline.WithStdoutLineContains(
			"Activity actor",
			"::0::1': invoking method 'Execute'",
			"activity completed for workflow with instanceId",
		),
	)

	b.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithScheduler(b.sched),
		daprd.WithAppID(appID1),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
		daprd.WithExecOptions(
			exec.WithStdout(wfOrchestrationLog.Stdout()),
		),
	)
	b.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithScheduler(b.sched),
		daprd.WithAppID(appID2),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
		daprd.WithExecOptions(
			exec.WithStdout(wfActivityLog.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(wfOrchestrationLog, wfActivityLog, b.place, b.sched, db, app1, app2, b.daprd1, b.daprd2),
	}
}

func (b *basiclogline) Run(t *testing.T, ctx context.Context) {
	b.sched.WaitUntilRunning(t, ctx)
	b.place.WaitUntilRunning(t, ctx)
	b.daprd1.WaitUntilRunning(t, ctx)
	b.daprd2.WaitUntilRunning(t, ctx)

	// Start workflow listeners for each app
	client1 := client.NewTaskHubGrpcClient(b.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	client2 := client.NewTaskHubGrpcClient(b.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())

	err := client1.StartWorkItemListener(ctx, b.registry1)
	assert.NoError(t, err)
	err = client2.StartWorkItemListener(ctx, b.registry2)
	assert.NoError(t, err)

	id, err := client1.ScheduleNewOrchestration(ctx, "CrossAppWorkflow", api.WithInput("Hello from app1"))
	require.NoError(t, err)

	// Wait for completion with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	metadata, err := client1.WaitForOrchestrationCompletion(waitCtx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, `"Processed by app2: Hello from app1"`, metadata.GetOutput().GetValue())
}
