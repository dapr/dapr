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
	suite.Register(new(requireandor))
}

// requireandor exercises AND nested within OR: two workflow rules for the same
// target workflow compose as OR, and the first rule's multi-event `requires`
// list composes as AND. Access is granted by "(fraud-check AND human-approval)
// OR vip-verified". A branch is satisfied only when ALL its events are present;
// partially satisfying the AND branch must not grant access.
type requireandor struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd

	targetWorkflowRuns atomic.Int64

	sensitiveChildWF string
	wfAndSatisfied   string
	wfAndPartial     string
	wfOrSatisfied    string
	wfNeither        string
}

func (r *requireandor) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)

	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	callerID := "caller-" + uuid.NewString()
	targetID := "target-" + uuid.NewString()
	ns := uuid.NewString()

	r.sensitiveChildWF = "sensitive-child"
	r.wfAndSatisfied = "wf-and-satisfied"
	r.wfAndPartial = "wf-and-partial"
	r.wfOrSatisfied = "wf-or-satisfied"
	r.wfNeither = "wf-neither"

	// "(fraud-check AND human-approval) OR vip-verified": the first rule's
	// requires list is an AND-set; the second rule is an OR alternative.
	policy := fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: requireandor-test
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
      - eventType: activity
        status: Completed
        name: fraud-check
      - eventType: activity
        status: Completed
        name: human-approval
    - name: %s
      operations:
      - schedule
      requires:
      - eventType: activity
        status: Completed
        name: vip-verified
`, targetID, callerID, r.sensitiveChildWF, r.sensitiveChildWF)

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

func (r *requireandor) Run(t *testing.T, ctx context.Context) {
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
	require.NoError(t, callerReg.AddActivityN("vip-verified", func(ctx task.ActivityContext) (any, error) {
		return "vip-ok", nil
	}))

	callChild := func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}

	// AND branch fully satisfied: both fraud-check and human-approval completed.
	require.NoError(t, callerReg.AddWorkflowN(r.wfAndSatisfied, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		if err := ctx.CallActivity("human-approval").Await(nil); err != nil {
			return nil, fmt.Errorf("human-approval failed: %w", err)
		}
		return callChild(ctx)
	}))

	// AND branch only partially satisfied: fraud-check completed but not
	// human-approval, and the OR alternative (vip-verified) is also absent.
	require.NoError(t, callerReg.AddWorkflowN(r.wfAndPartial, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		return callChild(ctx)
	}))

	// OR alternative satisfied: only vip-verified completed.
	require.NoError(t, callerReg.AddWorkflowN(r.wfOrSatisfied, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("vip-verified").Await(nil); err != nil {
			return nil, fmt.Errorf("vip-verified failed: %w", err)
		}
		return callChild(ctx)
	}))

	// Neither branch satisfied: no prerequisite activities completed.
	require.NoError(t, callerReg.AddWorkflowN(r.wfNeither, func(ctx *task.WorkflowContext) (any, error) {
		return callChild(ctx)
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

	t.Run("allowed when the AND branch is fully satisfied (fraud-check + human-approval)", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfAndSatisfied)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"should succeed when both AND events are satisfied: %v", metadata.GetFailureDetails())
		assert.Equal(t, before+1, r.targetWorkflowRuns.Load())
	})

	t.Run("allowed when the OR alternative is satisfied (vip-verified)", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfOrSatisfied)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"should succeed when the OR alternative is satisfied: %v", metadata.GetFailureDetails())
		assert.Equal(t, before+1, r.targetWorkflowRuns.Load())
	})

	t.Run("denied when the AND branch is only partially satisfied (fraud-check only)", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfAndPartial)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when the AND branch is only partially satisfied")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetWorkflowRuns.Load(),
			"sensitive child WF must NOT execute when the AND branch is incomplete")
	})

	t.Run("denied when neither branch is satisfied", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfNeither)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when neither branch is satisfied")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetWorkflowRuns.Load(),
			"sensitive child WF must NOT execute when no branch qualifies")
	})
}
