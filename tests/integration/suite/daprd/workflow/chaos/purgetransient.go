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

package chaos

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(purgetransient))
}

type purgetransient struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (p *purgetransient) Setup(t *testing.T) []framework.Option {
	appID := uuid.New().String()
	sen := sentry.New(t)
	p.scheduler = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	p.proxy = proxy.New(t, p.scheduler, proxy.WithSentry(t, sen, "default", appID))

	p.workflow = workflow.New(t,
		workflow.WithSentryInstance(sen),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithSchedulerInstance(p.scheduler),
		workflow.WithSchedulerAddress(p.proxy.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sen, p.scheduler, p.proxy, p.workflow),
	}
}

func (p *purgetransient) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("finishes", func(*dworkflow.WorkflowContext) (any, error) {
		return "finished", nil
	}))
	require.NoError(t, reg.AddWorkflowN("parked", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CreateTimer(time.Hour).Await(nil)
	}))

	client := p.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	db := p.workflow.DB().GetConnection(t)
	tableName := p.workflow.DB().TableName()
	rows := func(c *assert.CollectT, want int) {
		var count int
		require.NoError(c, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
		assert.Equal(c, want, count)
	}

	// Each purge deletes reminders with up to three DeleteByMetadata calls;
	// failing three in a row reaches every one of them at least once.
	const injected = 3

	t.Run("purge", func(t *testing.T) {
		id, err := client.ScheduleWorkflow(ctx, "finishes")
		require.NoError(t, err)
		_, err = client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)

		before := p.proxy.FailedCount()
		failed := make(chan struct{})
		p.proxy.ArmFailures(proxy.MethodDeleteByMetadata, injected, codes.Unavailable, failed)

		require.NoError(t, client.PurgeWorkflowState(ctx, id),
			"purge must ride out transient reminder delete failures")
		select {
		case <-failed:
		case <-time.After(10 * time.Second):
			require.Fail(t, "the injected DeleteByMetadata failure never fired")
		}
		assert.Equal(t, before+injected, p.proxy.FailedCount())

		_, err = client.FetchWorkflowMetadata(ctx, id)
		require.Error(t, err, "a purged instance must not be readable")
		assert.EventuallyWithT(t, func(c *assert.CollectT) { rows(c, 0) }, 20*time.Second, 10*time.Millisecond)
	})

	t.Run("force purge", func(t *testing.T) {
		id, err := client.ScheduleWorkflow(ctx, "parked")
		require.NoError(t, err)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, p.scheduler.ListAllKeys(t, ctx, "dapr/jobs"), 1, "the parked timer reminder")
		}, 10*time.Second, 10*time.Millisecond)

		before := p.proxy.FailedCount()
		failed := make(chan struct{})
		p.proxy.ArmFailures(proxy.MethodDeleteByMetadata, injected, codes.Unavailable, failed)

		require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)),
			"force purge must ride out transient reminder delete failures")
		select {
		case <-failed:
		case <-time.After(10 * time.Second):
			require.Fail(t, "the injected DeleteByMetadata failure never fired")
		}
		assert.Equal(t, before+injected, p.proxy.FailedCount())

		assert.EventuallyWithT(t, func(c *assert.CollectT) { rows(c, 0) }, 20*time.Second, 10*time.Millisecond)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, p.scheduler.ListAllKeys(t, ctx, "dapr/jobs"), "the timer reminder must not be orphaned")
		}, 10*time.Second, 10*time.Millisecond)
	})
}
