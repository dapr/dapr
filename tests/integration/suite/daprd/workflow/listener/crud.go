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
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(crud))
}

type crud struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
}

func (c *crud) Setup(t *testing.T) []framework.Option {
	place := placement.New(t)
	sched := scheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	c.daprd1 = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
	)

	c.daprd2 = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithScheduler(sched),
		daprd.WithAppID(c.daprd1.AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(place, sched, db, c.daprd1, c.daprd2),
	}
}

func (c *crud) Run(t *testing.T, ctx context.Context) {
	c.daprd1.WaitUntilRunning(t, ctx)
	c.daprd2.WaitUntilRunning(t, ctx)

	r := task.NewTaskRegistry()
	r.AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, ctx.CreateTimer(time.Second * 5).Await(nil)
	})
	r.AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	client1 := client.NewTaskHubGrpcClient(c.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, client1.StartWorkItemListener(ctx, r))

	client2 := client.NewTaskHubGrpcClient(c.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())

	_, err := client2.FetchOrchestrationMetadata(ctx, "foobar")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such instance exists")

	id, err := client2.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)
	_, err = client2.FetchOrchestrationMetadata(ctx, id)
	require.NoError(t, err)
	require.NoError(t, client2.SuspendOrchestration(ctx, id, "reason"))
	require.NoError(t, client2.ResumeOrchestration(ctx, id, "reason"))
	_, err = client2.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	id, err = client2.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)
	require.NoError(t, client2.TerminateOrchestration(ctx, id))
}
