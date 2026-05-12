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

package accesspolicy

import (
	"context"
	"fmt"
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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(deny))
}

// deny tests that cross-app workflow calls are rejected by default when a
// WorkflowAccessPolicy is loaded but no rule grants the caller access.
type deny struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
}

func (d *deny) Setup(t *testing.T) []framework.Option {
	d.sentry = sentry.New(t)

	d.place = placement.New(t, placement.WithSentry(t, d.sentry))
	d.sched = scheduler.New(t, scheduler.WithSentry(d.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	d.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	// Policy lists a different app as caller, so wfacl-caller is implicitly
	// denied when it tries to schedule a workflow on the target.
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: deny-test
scopes:
- wfacl-target
spec:
  rules:
  - callers:
    - appID: some-other-app
    workflows:
    - name: "*"
      operations: [schedule]
`)

	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	d.caller = daprd.New(t,
		daprd.WithAppID("wfacl-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(d.db.GetComponent(t)),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithSchedulerAddresses(d.sched.Address()),
		daprd.WithSentry(t, d.sentry),
	)
	d.target = daprd.New(t,
		daprd.WithAppID("wfacl-target"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(targetResDir),
		daprd.WithResourceFiles(d.db.GetComponent(t)),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithSchedulerAddresses(d.sched.Address()),
		daprd.WithSentry(t, d.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(d.sentry, d.place, d.sched, d.db, d.caller, d.target),
	}
}

func (d *deny) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)
	d.sched.WaitUntilRunning(t, ctx)
	d.caller.WaitUntilRunning(t, ctx)
	d.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()

	require.NoError(t, callerReg.AddWorkflowN("TestDeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("DeniedWF", task.WithChildWorkflowAppID(d.target.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("sub-orchestrator failed: %w", err)
		}
		return output, nil
	}))
	require.NoError(t, targetReg.AddWorkflowN("DeniedWF", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	callerClient := client.NewTaskHubGrpcClient(d.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(d.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(d.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(d.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	id, err := callerClient.ScheduleNewWorkflow(ctx, "TestDeniedWF")
	require.NoError(t, err)

	metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.NotNil(t, metadata.GetFailureDetails(),
		"orchestration should fail because the sub-orchestrator call is denied")
	assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
}
