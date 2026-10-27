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
	"strings"
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
	"github.com/dapr/durabletask-go/api"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(forcepurgemissing))
}

type forcepurgemissing struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (f *forcepurgemissing) Setup(t *testing.T) []framework.Option {
	appID := uuid.New().String()
	sen := sentry.New(t)
	f.scheduler = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	f.proxy = proxy.New(t, f.scheduler, proxy.WithSentry(t, sen, "default", appID))

	f.workflow = workflow.New(t,
		workflow.WithSentryInstance(sen),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithSchedulerInstance(f.scheduler),
		workflow.WithSchedulerAddress(f.proxy.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sen, f.scheduler, f.proxy, f.workflow),
	}
}

func (f *forcepurgemissing) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("parked", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CreateTimer(time.Hour).Await(nil)
	}))
	require.NoError(t, reg.AddWorkflowN("quick", func(*dworkflow.WorkflowContext) (any, error) {
		return "quick", nil
	}))

	client := f.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	db := f.workflow.DB().GetConnection(t)
	tableName := f.workflow.DB().TableName()

	serving := func(t *testing.T) {
		t.Helper()
		id, err := client.ScheduleWorkflow(ctx, "quick")
		require.NoError(t, err, "daprd must still accept workflows")
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, dworkflow.StatusCompleted, meta.RuntimeStatus)
	}

	t.Run("instance that never existed", func(t *testing.T) {
		err := client.PurgeWorkflowState(ctx, "never-existed", dworkflow.WithForcePurge(true))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "EOF", "daprd must answer, not drop the connection")
		serving(t)
	})

	t.Run("rows already purged, reminder left behind", func(t *testing.T) {
		id, err := client.ScheduleWorkflow(ctx, "parked")
		require.NoError(t, err)
		timer := func() []string {
			var keys []string
			for _, k := range f.scheduler.ListAllKeys(t, ctx, "dapr/jobs") {
				if strings.Contains(k, id) {
					keys = append(keys, k)
				}
			}
			return keys
		}
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, timer(), 1, "the parked timer reminder")
		}, 10*time.Second, 10*time.Millisecond)

		f.proxy.ArmFailures(proxy.MethodDeleteByMetadata, 3, codes.PermissionDenied, nil)
		require.Error(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))
		var count int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName+" WHERE key LIKE ?", "%"+id+"%").Scan(&count))
		require.Zero(t, count, "the partial purge deleted the rows")
		require.Len(t, timer(), 1, "the partial purge left the timer reminder")

		err = client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true))
		require.Error(t, err, "an instance without rows is not found")
		assert.NotContains(t, err.Error(), "EOF")
		assert.Contains(t, err.Error(), api.ErrInstanceNotFound.Error())
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, timer(), "force purge must remove the orphaned reminder")
		}, 10*time.Second, 10*time.Millisecond)
		serving(t)
	})
}
