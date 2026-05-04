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
	suite.Register(new(allow))
}

// allow tests that cross-app activity calls succeed when the
// WorkflowAccessPolicy explicitly allows the caller.
type allow struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
}

func (a *allow) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t)

	a.place = placement.New(t, placement.WithSentry(t, a.sentry))
	a.sched = scheduler.New(t, scheduler.WithSentry(a.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	a.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: allow-test
scopes:
- wfacl-target
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: wfacl-caller
    activities:
    - name: AllowedActivity
      action: allow
  - callers:
    - appID: wfacl-target
    activities:
    - name: "*"
      action: allow
`)

	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	a.caller = daprd.New(t,
		daprd.WithAppID("wfacl-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithSchedulerAddresses(a.sched.Address()),
		daprd.WithSentry(t, a.sentry),
	)
	a.target = daprd.New(t,
		daprd.WithAppID("wfacl-target"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(targetResDir),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithSchedulerAddresses(a.sched.Address()),
		daprd.WithSentry(t, a.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(a.sentry, a.place, a.sched, a.db, a.caller, a.target),
	}
}

func (a *allow) Run(t *testing.T, ctx context.Context) {
	a.place.WaitUntilRunning(t, ctx)
	a.sched.WaitUntilRunning(t, ctx)
	a.caller.WaitUntilRunning(t, ctx)
	a.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()

	require.NoError(t, callerReg.AddWorkflowN("AllowedWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallActivity("AllowedActivity", task.WithActivityAppID(a.target.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("activity failed: %w", err)
		}
		return output, nil
	}))
	require.NoError(t, targetReg.AddActivityN("AllowedActivity", func(ctx task.ActivityContext) (any, error) {
		return "allowed-result", nil
	}))

	callerClient := client.NewTaskHubGrpcClient(a.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(a.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(a.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(a.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	id, err := callerClient.ScheduleNewWorkflow(ctx, "AllowedWorkflow")
	require.NoError(t, err)

	metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Equal(t, `"allowed-result"`, metadata.GetOutput().GetValue())
}
