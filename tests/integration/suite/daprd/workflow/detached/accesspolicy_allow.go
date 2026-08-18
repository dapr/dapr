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

package detached

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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(accesspolicyAllow))
}

// accesspolicyAllow asserts that a cross-app detached spawn succeeds when
// the target's WorkflowAccessPolicy explicitly allows the caller's app.
type accesspolicyAllow struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
}

func (a *accesspolicyAllow) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t)
	a.place = placement.New(t, placement.WithSentry(t, a.sentry))
	a.sched = scheduler.New(t, scheduler.WithSentry(a.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	a.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: allow-detached
scopes:
- detached-target
spec:
  rules:
  - callers:
    - appID: detached-caller
    workflows:
    - name: AllowedSpawn
      operations: [schedule]
`)
	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	a.caller = daprd.New(t,
		daprd.WithAppID("detached-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(a.db.GetComponent(t)),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithSchedulerAddresses(a.sched.Address()),
		daprd.WithSentry(t, a.sentry),
	)
	a.target = daprd.New(t,
		daprd.WithAppID("detached-target"),
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

func (a *accesspolicyAllow) Run(t *testing.T, ctx context.Context) {
	a.place.WaitUntilRunning(t, ctx)
	a.sched.WaitUntilRunning(t, ctx)
	a.caller.WaitUntilRunning(t, ctx)
	a.target.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-allow"

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()

	require.NoError(t, callerReg.AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("AllowedSpawn",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID),
			task.WithDetachedWorkflowAppID(a.target.AppID()),
		)
		return nil, err
	}))
	require.NoError(t, targetReg.AddWorkflowN("AllowedSpawn", func(ctx *task.WorkflowContext) (any, error) {
		return "spawned-output", nil
	}))

	callerClient := client.NewTaskHubGrpcClient(a.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(a.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(a.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(a.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, 20*time.Second, 10*time.Millisecond)

	parentID, err := callerClient.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)
	parentMeta, err := callerClient.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	spawnedMeta, err := targetClient.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID),
		api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMeta.GetRuntimeStatus())
	assert.Equal(t, `"spawned-output"`, spawnedMeta.GetOutput().GetValue())
}
