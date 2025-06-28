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

	"github.com/stretchr/testify/assert"

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
	suite.Register(new(invalidappid))
}

// invalidappid tests error handling when calling activities on non-existent app IDs
type invalidappid struct {
	daprd1 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	actorNotFoundLogLine *logline.LogLine
	registry1            *task.TaskRegistry
}

func (i *invalidappid) Setup(t *testing.T) []framework.Option {
	i.place = placement.New(t)
	i.sched = scheduler.New(t,
		scheduler.WithLogLevel("debug"))
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	app1 := app.New(t)

	// Create registry for app1 only
	i.registry1 = task.NewTaskRegistry()

	// App1: Orchestrator that tries to call non-existent apps
	i.registry1.AddOrchestratorN("InvalidAppWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		// Try to call activity on non-existent app
		var result string
		err := ctx.CallActivity("ProcessData",
			task.WithActivityInput(input),
			task.WithAppID("nonexistent-app")).
			Await(&result)

		// This should fail, so we expect an error
		if err == nil {
			return nil, fmt.Errorf("expected error when calling non-existent app, but got none")
		}
		return fmt.Sprintf("Error handled: %v", err), nil
	})

	i.actorNotFoundLogLine = logline.New(t,
		logline.WithStdoutLineContains(
			"did not find address for actor",
		),
	)

	i.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(i.place.Address()),
		daprd.WithScheduler(i.sched),
		daprd.WithAppID("app1"),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
		daprd.WithExecOptions(
			exec.WithStdout(i.actorNotFoundLogLine.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(i.place, i.sched, i.actorNotFoundLogLine, db, app1, i.daprd1),
	}
}

func (i *invalidappid) Run(t *testing.T, ctx context.Context) {
	i.sched.WaitUntilRunning(t, ctx)
	i.place.WaitUntilRunning(t, ctx)
	i.daprd1.WaitUntilRunning(t, ctx)

	// Start workflow listener for app1
	client1 := client.NewTaskHubGrpcClient(i.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())

	err := client1.StartWorkItemListener(ctx, i.registry1)
	assert.NoError(t, err)

	// ctx cancel bc it will hang
	wCtx, wcancel := context.WithTimeout(ctx, 20*time.Second)
	defer wcancel()
	_, err = client1.ScheduleNewOrchestration(wCtx, "InvalidAppWorkflow", api.WithInput("Hello from app1"))
	assert.Error(t, err)
	assert.EqualError(t, err, "context deadline exceeded")

	i.actorNotFoundLogLine.EventuallyFoundAll(t)
}
