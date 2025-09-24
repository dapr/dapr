/*
Copyright 2024 The Dapr Authors
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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(fanoutthree))
}

type fanoutthree struct {
	called slice.Slice[int]
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	daprd3 *daprd.Daprd
}

func (f *fanoutthree) Setup(t *testing.T) []framework.Option {
	f.called = slice.New[int]()

	placement := placement.New(t)
	scheduler := scheduler.New(t)

	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	db.GetComponent(t)
	f.daprd1 = daprd.New(t,
		daprd.WithPlacementAddresses(placement.Address()),
		daprd.WithScheduler(scheduler),
		daprd.WithResourceFiles(db.GetComponent(t)),
	)
	f.daprd2 = daprd.New(t,
		daprd.WithPlacementAddresses(placement.Address()),
		daprd.WithScheduler(scheduler),
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithAppID(f.daprd1.AppID()),
	)
	f.daprd3 = daprd.New(t,
		daprd.WithPlacementAddresses(placement.Address()),
		daprd.WithScheduler(scheduler),
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithAppID(f.daprd1.AppID()),
	)

	return []framework.Option{
		framework.WithProcesses(placement, scheduler, db, f.daprd1, f.daprd2, f.daprd3),
	}
}

func (f *fanoutthree) Run(t *testing.T, ctx context.Context) {
	f.daprd1.WaitUntilRunning(t, ctx)
	f.daprd2.WaitUntilRunning(t, ctx)
	f.daprd3.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()

	registry.AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		tasks := make([]task.Task, 5)
		for i := range 5 {
			tasks[i] = ctx.CallActivity("bar", task.WithActivityInput(i))
		}

		var errs []error
		for _, task := range tasks {
			errs = append(errs, task.Await(nil))
		}
		return nil, errors.Join(errs...)
	})
	registry.AddActivityN("bar", func(ctx task.ActivityContext) (any, error) {
		var ii int
		if err := ctx.GetInput(&ii); err != nil {
			return nil, err
		}
		f.called.Append(ii)
		return nil, nil
	})

	client1 := client.NewTaskHubGrpcClient(f.daprd1.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client1.StartWorkItemListener(ctx, registry))
	client2 := client.NewTaskHubGrpcClient(f.daprd2.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client2.StartWorkItemListener(ctx, registry))
	client3 := client.NewTaskHubGrpcClient(f.daprd3.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client3.StartWorkItemListener(ctx, registry))

	id, err := client1.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)
	_, err = client1.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)
	exp := make([]int, 5)
	for i := range 5 {
		exp[i] = i
	}
	assert.ElementsMatch(t, exp, f.called.Slice())
}
