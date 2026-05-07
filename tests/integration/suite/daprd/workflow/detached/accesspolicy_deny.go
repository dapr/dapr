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
	suite.Register(new(accesspolicyDeny))
}

// accesspolicyDeny asserts that when a target's WorkflowAccessPolicy denies
// the caller's app, the cross-app detached spawn is rejected and the parent
// is failed terminally with the denial as its FailureDetails. Detached
// spawns are fire-and-forget, so the caller's only surface for observing
// the denial is the parent's terminal status — silently dropping it would
// hide the policy violation, and retrying it would loop forever.
type accesspolicyDeny struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd
}

func (d *accesspolicyDeny) Setup(t *testing.T) []framework.Option {
	d.sentry = sentry.New(t)
	d.place = placement.New(t, placement.WithSentry(t, d.sentry))
	d.sched = scheduler.New(t, scheduler.WithSentry(d.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	d.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: deny-detached
scopes:
- detached-deny-target
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
		daprd.WithAppID("detached-deny-caller"),
		daprd.WithNamespace("default"),
		daprd.WithResourceFiles(d.db.GetComponent(t)),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithSchedulerAddresses(d.sched.Address()),
		daprd.WithSentry(t, d.sentry),
	)
	d.target = daprd.New(t,
		daprd.WithAppID("detached-deny-target"),
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

func (d *accesspolicyDeny) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)
	d.sched.WaitUntilRunning(t, ctx)
	d.caller.WaitUntilRunning(t, ctx)
	d.target.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-deny"

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()

	require.NoError(t, callerReg.AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("DeniedSpawn",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID),
			task.WithDetachedWorkflowAppID(d.target.AppID()),
		)
		return "caller-completed", err
	}))
	require.NoError(t, targetReg.AddWorkflowN("DeniedSpawn", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	callerClient := client.NewTaskHubGrpcClient(d.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(d.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(d.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(d.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, 20*time.Second, 10*time.Millisecond)

	parentID, err := callerClient.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)
	parentMeta, err := callerClient.WaitForWorkflowCompletion(ctx, parentID, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, parentMeta.GetRuntimeStatus(),
		"parent must fail terminally so the policy denial is observable on the caller's status")
	require.NotNil(t, parentMeta.GetFailureDetails(),
		"failed parent must carry FailureDetails describing the denial")
	assert.Equal(t, "WorkflowAccessPolicyDenied", parentMeta.GetFailureDetails().GetErrorType())
	assert.Contains(t, parentMeta.GetFailureDetails().GetErrorMessage(), spawnedInstanceID)
	assert.Contains(t, parentMeta.GetFailureDetails().GetErrorMessage(), d.target.AppID())
	assert.Contains(t, parentMeta.GetFailureDetails().GetErrorMessage(), "denied by access policy")

	// Spawn must NOT have been created on the target — the dispatch was
	// rejected before the target's actor could materialize.
	_, fetchErr := targetClient.FetchWorkflowMetadata(ctx, api.InstanceID(spawnedInstanceID))
	require.Error(t, fetchErr, "spawn must NOT have been created on the deny-target app")
}
