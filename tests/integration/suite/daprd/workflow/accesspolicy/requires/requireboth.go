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

package requires

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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
	suite.Register(new(requireboth))
}

// Multiple items inside a single `requires` list compose as AND: every
// listed event must be present in propagated history.
type requireboth struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd

	targetWorkflowRuns atomic.Int64

	sensitiveChildWF string
	wfBoth           string
	wfOnlyFraud      string
	wfOnlyApproval   string
	wfNeither        string
}

func (r *requireboth) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)

	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	callerID := "caller-" + uuid.NewString()
	targetID := "target-" + uuid.NewString()
	ns := uuid.NewString()

	r.sensitiveChildWF = "sensitive-child"
	r.wfBoth = "wf-both"
	r.wfOnlyFraud = "wf-only-fraud"
	r.wfOnlyApproval = "wf-only-approval"
	r.wfNeither = "wf-neither"

	// Single schedule entry, two requires items. Both must be present in
	// propagated history for the rule to apply.
	policy := fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: requireboth-test
scopes:
- %s
spec:
  rules:
  - callers:
    - appID: %s
    workflows:
    - name: %s
      operations:
      - name: schedule
        requires:
        - eventType: activity
          status: Completed
          name: fraud-check
        - eventType: activity
          status: Completed
          name: human-approval
`, targetID, callerID, r.sensitiveChildWF)

	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	r.caller = daprd.New(t,
		daprd.WithAppID(callerID),
		daprd.WithNamespace(ns),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithSentry(t, r.sentry),
	)
	r.target = daprd.New(t,
		daprd.WithAppID(targetID),
		daprd.WithNamespace(ns),
		daprd.WithResourcesDir(targetResDir),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithSentry(t, r.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(r.sentry, r.place, r.sched, r.db, r.caller, r.target),
	}
}

func (r *requireboth) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.caller.WaitUntilRunning(t, ctx)
	r.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()
	targetAppID := r.target.AppID()

	require.NoError(t, callerReg.AddActivityN("fraud-check", func(ctx task.ActivityContext) (any, error) {
		return "fraud-ok", nil
	}))
	require.NoError(t, callerReg.AddActivityN("human-approval", func(ctx task.ActivityContext) (any, error) {
		return "approved", nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfBoth, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		if err := ctx.CallActivity("human-approval").Await(nil); err != nil {
			return nil, fmt.Errorf("human-approval failed: %w", err)
		}
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfOnlyFraud, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfOnlyApproval, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("human-approval").Await(nil); err != nil {
			return nil, fmt.Errorf("human-approval failed: %w", err)
		}
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfNeither, func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, targetReg.AddWorkflowN(r.sensitiveChildWF, func(ctx *task.WorkflowContext) (any, error) {
		r.targetWorkflowRuns.Add(1)
		return "sensitive-completed", nil
	}))

	callerClient := client.NewTaskHubGrpcClient(r.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(r.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(r.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, 20*time.Second, 10*time.Millisecond)

	t.Run("allowed when both required activities are present", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfBoth)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"caller should succeed when both requires items are satisfied: %v", metadata.GetFailureDetails())
		assert.Equal(t, before+1, r.targetWorkflowRuns.Load())
	})

	t.Run("denied when only fraud-check is present", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfOnlyFraud)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when human-approval is missing from history (AND requires both)")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetWorkflowRuns.Load())
	})

	t.Run("denied when only human-approval is present", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfOnlyApproval)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when fraud-check is missing from history (AND requires both)")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetWorkflowRuns.Load())
	})

	t.Run("denied when neither required activity is present", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfNeither)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when neither requires item is satisfied")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetWorkflowRuns.Load())
	})
}
