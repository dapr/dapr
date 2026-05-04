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
	suite.Register(new(reminderbypass))
}

// reminderbypass tests that an unauthorized sidecar cannot bypass workflow
// access policies by crafting reminders. An attacker app not in any policy
// rule attempts to schedule cross-app workflows and activities on a target
// app; all attempts are denied.
type reminderbypass struct {
	sentry   *sentry.Sentry
	place    *placement.Placement
	sched    *scheduler.Scheduler
	db       *sqlite.SQLite
	target   *daprd.Daprd
	attacker *daprd.Daprd
}

func (r *reminderbypass) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)

	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	// Policy only allows "legit-caller". "attacker-app" is not in any rule.
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: bypass-test
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: legit-caller
    operations:
    - type: workflow
      name: "*"
      action: allow
    - type: activity
      name: "*"
      action: allow
  - callers:
    - appID: bypass-target
    operations:
    - type: activity
      name: "*"
      action: allow
`)

	// Both sidecars load the policy: the attacker's local router check also
	// denies, and the target's remote CallActor check denies.
	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))
	attackerResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(attackerResDir, "policy.yaml"), policy, 0o600))

	r.target = daprd.New(t,
		daprd.WithAppID("bypass-target"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(targetResDir),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithSentry(t, r.sentry),
	)
	r.attacker = daprd.New(t,
		daprd.WithAppID("attacker-app"),
		daprd.WithNamespace("default"),
		daprd.WithResourcesDir(attackerResDir),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithSentry(t, r.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(r.sentry, r.place, r.sched, r.db, r.target, r.attacker),
	}
}

func (r *reminderbypass) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.target.WaitUntilRunning(t, ctx)
	r.attacker.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("AttackWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow("VictimWorkflow", task.WithChildWorkflowAppID(r.target.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("attack failed: %w", err)
		}
		return output, nil
	}))
	require.NoError(t, registry.AddWorkflowN("AttackActivity", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallActivity("VictimActivity", task.WithActivityAppID(r.target.AppID())).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("attack failed: %w", err)
		}
		return output, nil
	}))

	targetRegistry := task.NewTaskRegistry()
	require.NoError(t, targetRegistry.AddWorkflowN("VictimWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, targetRegistry.AddActivityN("VictimActivity", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	}))

	attackerClient := client.NewTaskHubGrpcClient(r.attacker.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, attackerClient.StartWorkItemListener(ctx, registry))
	targetClient := client.NewTaskHubGrpcClient(r.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetRegistry))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.attacker.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(r.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("attacker cannot schedule cross-app workflow on target", func(t *testing.T) {
		id, err := attackerClient.ScheduleNewWorkflow(ctx, "AttackWorkflow")
		if err != nil {
			assert.Contains(t, err.Error(), "PermissionDenied")
			return
		}

		metadata, err := attackerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"attacker should not be able to schedule workflows on target")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})

	t.Run("attacker cannot call cross-app activity on target", func(t *testing.T) {
		id, err := attackerClient.ScheduleNewWorkflow(ctx, "AttackActivity")
		if err != nil {
			assert.Contains(t, err.Error(), "PermissionDenied")
			return
		}

		metadata, err := attackerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"attacker should not be able to call activities on target")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})
}
