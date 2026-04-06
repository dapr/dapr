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

package childwf

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
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
	suite.Register(new(inboxpreserved))
}

type inboxpreserved struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry
}

func (p *inboxpreserved) Setup(t *testing.T) []framework.Option {
	p.place = placement.New(t)
	p.sched = scheduler.New(t)
	p.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
		sqlite.WithMetadata("busyTimeout", "10s"),
	)

	app1 := app.New(t)
	app2 := app.New(t)

	p.registry1 = task.NewTaskRegistry()
	p.registry2 = task.NewTaskRegistry()

	p.daprd1 = daprd.New(t,
		daprd.WithResourceFiles(p.db.GetComponent(t)),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithScheduler(p.sched),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)
	p.daprd2 = daprd.New(t,
		daprd.WithResourceFiles(p.db.GetComponent(t)),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithScheduler(p.sched),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
	)

	p.registry1.AddWorkflowN("ParentWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var result string
		if err := ctx.CallChildWorkflow("ChildWorkflow",
			task.WithChildWorkflowInput(input),
			task.WithChildWorkflowAppID(p.daprd2.AppID())).
			Await(&result); err != nil {
			return nil, fmt.Errorf("child workflow failed: %w", err)
		}
		return result, nil
	})

	p.registry2.AddWorkflowN("ChildWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return "child:" + input, nil
	})

	return []framework.Option{
		framework.WithProcesses(p.place, p.sched, p.db, app1, app2, p.daprd1),
	}
}

func (p *inboxpreserved) Run(t *testing.T, ctx context.Context) {
	p.sched.WaitUntilRunning(t, ctx)
	p.place.WaitUntilRunning(t, ctx)
	p.daprd1.WaitUntilRunning(t, ctx)

	client1 := client.NewTaskHubGrpcClient(p.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, p.registry1))

	id, err := client1.ScheduleNewWorkflow(ctx, "ParentWorkflow",
		api.WithInput("hello"), api.WithInstanceID("test-childwf-inbox"))
	require.NoError(t, err)

	metadata, err := client1.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, metadata.RuntimeStatus)

	time.Sleep(5 * time.Second)

	db := p.db.GetConnection(t)
	tableName := p.db.GetTableName(t)

	var inboxCount int
	require.NoError(t, db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE key LIKE '%%||test-childwf-inbox||inbox-%%'", tableName),
	).Scan(&inboxCount))
	assert.GreaterOrEqual(t, inboxCount, 1, "inbox should be preserved after retry cycles")

	var historyCount int
	require.NoError(t, db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE key LIKE '%%||test-childwf-inbox||history-%%'", tableName),
	).Scan(&historyCount))
	assert.Equal(t, 2, historyCount, "history should contain exactly 2 entries from the pre-save (OrchestratorStarted + ExecutionStarted), not grow with retries")

	p.daprd2.Run(t, ctx)
	t.Cleanup(func() { p.daprd2.Cleanup(t) })
	p.daprd2.WaitUntilRunning(t, ctx)

	client2 := client.NewTaskHubGrpcClient(p.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client2.StartWorkItemListener(ctx, p.registry2))

	metadata, err = client1.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"child:hello"`, metadata.GetOutput().GetValue())
}
