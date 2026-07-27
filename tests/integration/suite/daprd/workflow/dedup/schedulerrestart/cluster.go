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

package schedulerrestart

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(schedulercluster))
}

type schedulercluster struct {
	db         *sqlite.SQLite
	place      *placement.Placement
	schedulers []*scheduler.Scheduler
	daprd      *daprd.Daprd
	registry   *task.TaskRegistry
}

func (s *schedulercluster) Setup(t *testing.T) []framework.Option {
	s.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	s.place = placement.New(t)

	const n = 3
	fp := ports.Reserve(t, 2*n)
	peer := make([]int, n)
	clientPort := make([]int, n)
	parts := make([]string, n)
	for i := range n {
		peer[i] = fp.Port(t)
		clientPort[i] = fp.Port(t)
		parts[i] = fmt.Sprintf("sched-%d=http://127.0.0.1:%d", i, peer[i])
	}
	initial := fmt.Sprintf("%s,%s,%s", parts[0], parts[1], parts[2])

	s.schedulers = make([]*scheduler.Scheduler, n)
	for i := range n {
		s.schedulers[i] = scheduler.New(t,
			scheduler.WithID(fmt.Sprintf("sched-%d", i)),
			scheduler.WithInitialCluster(initial),
			scheduler.WithEtcdClientPort(clientPort[i]),
		)
	}

	s.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
		daprd.WithSchedulerAddresses(
			s.schedulers[0].Address(),
			s.schedulers[1].Address(),
			s.schedulers[2].Address(),
		),
	)
	s.registry = task.NewTaskRegistry()

	return []framework.Option{
		framework.WithProcesses(fp, s.db, s.place,
			s.schedulers[0], s.schedulers[1], s.schedulers[2], s.daprd),
	}
}

func (s *schedulercluster) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	for i := range s.schedulers {
		s.schedulers[i].WaitUntilRunning(t, ctx)
	}
	s.daprd.WaitUntilRunning(t, ctx)

	var calls atomic.Int32
	holdCh := make(chan struct{})

	require.NoError(t, s.registry.AddWorkflowN("wf",
		func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity("act").Await(nil)
		}))
	require.NoError(t, s.registry.AddActivityN("act",
		func(ctx task.ActivityContext) (any, error) {
			calls.Add(1)
			select {
			case <-holdCh:
			case <-ctx.Context().Done():
			}
			return nil, nil
		}))

	cl := client.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, s.registry))

	id, err := cl.ScheduleNewWorkflow(ctx, "wf")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), calls.Load())
	}, 30*time.Second, 10*time.Millisecond)

	// Gracefully restart one of three; the remaining two keep quorum so
	// cron continues firing and we can exercise the at-least-once
	// redelivery path against a still-active cluster.
	s.schedulers[0].RestartGraceful(t, ctx)

	require.Never(t, func() bool {
		return calls.Load() > 1
	}, 5*time.Second, 100*time.Millisecond)

	close(holdCh)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, int32(1), calls.Load())
}
