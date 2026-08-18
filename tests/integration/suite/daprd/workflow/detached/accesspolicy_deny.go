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
// the caller's app, the cross-app detached spawn is dropped and the caller
// is unaffected. Detached workflows are fire-and-forget by design: a denied
// spawn must not propagate any failure back to the parent — the policy
// merely prevents the spawn from materializing on the target.
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

	// Parent must complete successfully — detached spawns are
	// fire-and-forget, so a policy denial on the target cannot surface as
	// a parent failure.
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, parentMeta.GetRuntimeStatus(),
		"parent must complete COMPLETED — the denial is fire-and-forget and must not affect the caller")
	require.Nil(t, parentMeta.GetFailureDetails(),
		"parent must carry no FailureDetails: detached denials do not flow back")
	assert.Equal(t, `"caller-completed"`, parentMeta.GetOutput().GetValue(),
		"parent must surface its own return value, not anything from the denied spawn")

	// Caller history still records the audit event for the attempted
	// spawn — the action was emitted, it just didn't land on the target.
	hist, err := callerClient.GetInstanceHistory(ctx, parentID)
	require.NoError(t, err)
	var detachedCount int
	for _, e := range hist.GetEvents() {
		if dw := e.GetDetachedWorkflowInstanceCreated(); dw != nil && dw.GetInstanceId() == spawnedInstanceID {
			detachedCount++
		}
	}
	assert.Equal(t, 1, detachedCount,
		"caller history must record the attempted detached spawn even though it was denied on the target")

	// The spawn was rejected before the target actor could materialize.
	_, fetchErr := targetClient.FetchWorkflowMetadata(ctx, api.InstanceID(spawnedInstanceID))
	require.Error(t, fetchErr, "spawn must NOT have been created on the deny-target app")
}
