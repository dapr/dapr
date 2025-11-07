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

package force

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(newreplica))
}

type newreplica struct {
	db     *sqlite.SQLite
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler
}

func (n *newreplica) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t)
	n.sched = scheduler.New(t)

	n.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	n.daprd1 = daprd.New(t,
		daprd.WithPlacement(n.place),
		daprd.WithScheduler(n.sched),
		daprd.WithResourceFiles(n.db.GetComponent(t)),
	)
	n.daprd2 = daprd.New(t,
		daprd.WithPlacement(n.place),
		daprd.WithScheduler(n.sched),
		daprd.WithResourceFiles(n.db.GetComponent(t)),
		daprd.WithAppID(n.daprd1.AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(n.place, n.sched, n.db, n.daprd1),
	}
}

func (n *newreplica) Run(t *testing.T, ctx context.Context) {
	n.daprd1.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CreateTimer(time.Minute*60).Await(nil))
		return nil, nil
	})

	client := dworkflow.NewClient(n.daprd1.GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "purge")
	require.NoError(t, err)

	db := n.db.GetConnection(t)
	tableName := n.db.TableName()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Positive(t, count)

	n.daprd1.Kill(t)
	n.daprd2.Run(t, ctx)
	t.Cleanup(func() {
		n.daprd2.Kill(t)
	})

	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Positive(t, count)

	client = dworkflow.NewClient(n.daprd2.GRPCConn(t, ctx))
	require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))

	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 0, count)
}
