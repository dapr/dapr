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

package childnotify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(policydenied))
}

// policydenied gives the parent's app a WorkflowAccessPolicy that does not
// admit the child's app. The child's completion is refused; it must keep
// retrying rather than count the refusal as delivered, and land once the
// policy is removed.
type policydenied struct {
	sentry    *sentry.Sentry
	place     *placement.Placement
	sched     *scheduler.Scheduler
	db        *sqlite.SQLite
	parent    *daprd.Daprd
	child     *daprd.Daprd
	policyDir string
}

func (p *policydenied) Setup(t *testing.T) []framework.Option {
	p.sentry = sentry.New(t)
	p.place = placement.New(t, placement.WithSentry(t, p.sentry))
	p.sched = scheduler.New(t, scheduler.WithSentry(p.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	p.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	p.policyDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(p.policyDir, "policy.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: parent-only
scopes:
- notify-parent
spec:
  rules:
  - callers:
    - appID: some-other-app
    workflows:
    - name: "*"
      operations: [schedule]
`), 0o600))

	p.parent = daprd.New(t,
		daprd.WithAppID("notify-parent"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(p.policyDir),
		daprd.WithResourceFiles(p.db.GetComponent(t)),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithSchedulerAddresses(p.sched.Address()),
		daprd.WithSentry(t, p.sentry),
	)
	p.child = daprd.New(t,
		daprd.WithAppID("notify-child"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(p.db.GetComponent(t)),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithSchedulerAddresses(p.sched.Address()),
		daprd.WithSentry(t, p.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(p.sentry, p.place, p.sched, p.db, p.parent, p.child),
	}
}

func (p *policydenied) Run(t *testing.T, ctx context.Context) {
	p.place.WaitUntilRunning(t, ctx)
	p.sched.WaitUntilRunning(t, ctx)
	p.parent.WaitUntilRunning(t, ctx)
	p.child.WaitUntilRunning(t, ctx)

	const childID = "policydenied-child"
	parentReg := task.NewTaskRegistry()
	childReg := task.NewTaskRegistry()
	require.NoError(t, parentReg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("child", task.WithChildWorkflowAppID(p.child.AppID()), task.WithChildWorkflowInstanceID(childID)).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, childReg.AddWorkflowN("child", func(*task.WorkflowContext) (any, error) {
		return "admitted", nil
	}))

	parentClient := client.NewTaskHubGrpcClient(p.parent.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, parentClient.StartWorkItemListener(ctx, parentReg))
	childClient := client.NewTaskHubGrpcClient(p.child.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, childClient.StartWorkItemListener(ctx, childReg))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(p.parent.GetMetaActorRuntime(c, ctx).ActiveActors), 1)
		assert.GreaterOrEqual(c, len(p.child.GetMetaActorRuntime(c, ctx).ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	id, err := parentClient.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)
	wf.WaitForRuntimeStatus(t, ctx, childClient, childID, api.RUNTIME_STATUS_COMPLETED)

	// Refused, not delivered: the retry reminder stays and the parent waits.
	p.sched.WaitJobKeyCount(t, ctx, "parent-notify", func(n int) bool { return n > 0 })
	time.Sleep(time.Second * 2)
	assert.Positive(t, p.sched.JobKeyCount(t, ctx, "parent-notify"), "a refusal is not an acknowledgement")
	meta, err := parentClient.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())

	// Lifting the policy lets the retry land.
	require.NoError(t, os.Remove(filepath.Join(p.policyDir, "policy.yaml")))
	meta, err = parentClient.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"admitted"`, meta.GetOutput().GetValue())
	p.sched.WaitJobKeyCount(t, ctx, "parent-notify", func(n int) bool { return n == 0 })
}
