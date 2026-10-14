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

package resultreminder

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(orphan))
}

type orphan struct {
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (o *orphan) Setup(t *testing.T) []framework.Option {
	o.place = placement.New(t)
	o.scheduler = procscheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(o.place, o.scheduler),
	}
}

func (o *orphan) Run(t *testing.T, ctx context.Context) {
	o.scheduler.WaitUntilRunning(t, ctx)
	o.place.WaitUntilRunning(t, ctx)

	appA := uuid.New().String()
	appB := uuid.New().String()

	newDaprd := func(appID string) *daprd.Daprd {
		return daprd.New(t,
			daprd.WithAppID(appID),
			daprd.WithInMemoryActorStateStore("statestore"),
			daprd.WithPlacementAddresses(o.place.Address()),
			daprd.WithSchedulerAddresses(o.scheduler.Address()),
		)
	}

	daprdA := newDaprd(appA)
	daprdB := newDaprd(appB)
	daprdA.Run(t, ctx)
	daprdB.Run(t, ctx)
	t.Cleanup(func() { daprdB.Cleanup(t) })
	daprdA.WaitUntilRunning(t, ctx)
	daprdB.WaitUntilRunning(t, ctx)

	regA := task.NewTaskRegistry()
	require.NoError(t, regA.AddWorkflowN("Orphaned", func(c *task.WorkflowContext) (any, error) {
		var out string
		err := c.CallActivity("Slow",
			task.WithActivityInput("x"),
			task.WithActivityAppID(appB),
		).Await(&out)
		return out, err
	}))

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	started := make(chan struct{}, 1)
	regB := task.NewTaskRegistry()
	require.NoError(t, regB.AddActivityN("Slow", func(c task.ActivityContext) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return "done", nil
	}))

	clientA := client.NewTaskHubGrpcClient(daprdA.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, clientA.StartWorkItemListener(ctx, regA))
	clientB := client.NewTaskHubGrpcClient(daprdB.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, clientB.StartWorkItemListener(ctx, regB))

	_, err := daprdA.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "Orphaned",
	})
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	daprdA.Cleanup(t)
	block <- struct{}{}

	activityResultJobCount := func(t *testing.T, ctx context.Context, s *procscheduler.Scheduler) int {
		t.Helper()
		var count int
		for _, key := range s.ListAllKeys(t, ctx, "dapr/jobs") {
			if strings.Contains(key, "activity-result") {
				count++
			}
		}
		return count
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, activityResultJobCount(t, ctx, o.scheduler), 1,
			"the undeliverable result must be queued as an activity-result reminder")
	}, time.Second*30, time.Millisecond*10)

	daprdA2 := newDaprd(appA)
	daprdA2.Run(t, ctx)
	t.Cleanup(func() { daprdA2.Cleanup(t) })
	daprdA2.WaitUntilRunning(t, ctx)

	clientA2 := client.NewTaskHubGrpcClient(daprdA2.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, clientA2.StartWorkItemListener(ctx, regA))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, activityResultJobCount(t, ctx, o.scheduler),
			"an activity-result reminder for a missing instance must be acked and deleted, not retried")
	}, time.Second*30, time.Millisecond*10)
}
