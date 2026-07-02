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
	suite.Register(new(requireeither))
}

// Two workflow rules with the same name but different `requires` blocks
// compose as OR across rules: completing either path's prerequisites is
// sufficient. Within each rule's `requires` list, the items still compose as AND.
type requireeither struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd

	targetWorkflowRuns atomic.Int64

	sensitiveChildWF string
	wfPathA          string
	wfPathB          string
	wfBoth           string
	wfNeither        string
}

func (r *requireeither) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)

	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	callerID := "caller-" + uuid.NewString()
	targetID := "target-" + uuid.NewString()
	ns := uuid.NewString()

	r.sensitiveChildWF = "sensitive-child"
	r.wfPathA = "wf-path-a"
	r.wfPathB = "wf-path-b"
	r.wfBoth = "wf-both"
	r.wfNeither = "wf-neither"

	// Two schedule rules for the same target workflow, gated on different
	// prerequisite activities. Either path's completion is sufficient.
	policy := fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: requireeither-test
scopes:
- %s
spec:
  rules:
  - callers:
    - appID: %s
    workflows:
    - name: %s
      operations:
      - schedule
      requires:
      - eventType: activity.completed
        name: fraud-check
        appID: %s
    - name: %s
      operations:
      - schedule
      requires:
      - eventType: activity.completed
        name: vip-verified
        appID: %s
`, targetID, callerID, r.sensitiveChildWF, callerID, r.sensitiveChildWF, callerID)

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

func (r *requireeither) Run(t *testing.T, ctx context.Context) {
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
	require.NoError(t, callerReg.AddActivityN("vip-verified", func(ctx task.ActivityContext) (any, error) {
		return "vip-ok", nil
	}))

	// Path A: only fraud-check completed, schedules the sensitive child WF.
	require.NoError(t, callerReg.AddWorkflowN(r.wfPathA, func(ctx *task.WorkflowContext) (any, error) {
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

	// Path B: only vip-verified completed.
	require.NoError(t, callerReg.AddWorkflowN(r.wfPathB, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("vip-verified").Await(nil); err != nil {
			return nil, fmt.Errorf("vip-verified failed: %w", err)
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

	// Both prereqs completed. Evaluator should short-circuit on the first
	// matching entry; the target workflow must run exactly once.
	require.NoError(t, callerReg.AddWorkflowN(r.wfBoth, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		if err := ctx.CallActivity("vip-verified").Await(nil); err != nil {
			return nil, fmt.Errorf("vip-verified failed: %w", err)
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

	// Neither prerequisite completed, propagated history doesn't satisfy
	// either entry's requires.
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

	t.Run("allowed when only path A's requires is satisfied (fraud-check completed)", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfPathA)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"path A should succeed when its requires is satisfied: %v", metadata.GetFailureDetails())
		assert.Equal(t, before+1, r.targetWorkflowRuns.Load())
	})

	t.Run("allowed when only path B's requires is satisfied (vip-verified completed)", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfPathB)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"path B should succeed when its requires is satisfied: %v", metadata.GetFailureDetails())
		assert.Equal(t, before+1, r.targetWorkflowRuns.Load())
	})

	t.Run("allowed when both paths' requires are satisfied; first matching entry wins", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfBoth)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"caller should succeed when both paths' requires are satisfied: %v", metadata.GetFailureDetails())
		assert.Equal(t, before+1, r.targetWorkflowRuns.Load(),
			"target should run exactly once even when multiple entries match (evaluator short-circuits on first match)")
	})

	t.Run("denied when neither path's requires is satisfied", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfNeither)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when neither path's requires is satisfied")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetWorkflowRuns.Load(),
			"sensitive child WF must NOT execute on target when neither path qualifies")
	})
}
