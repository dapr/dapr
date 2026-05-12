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
	suite.Register(new(activity))
}

// requires matching on eventType=activity (TaskCompleted in history).
type activity struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd

	targetActivityRuns atomic.Int64

	gatedActivity   string
	wfWithPreflight string
	wfNoPreflight   string
}

func (r *activity) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)

	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	callerID := "caller-" + uuid.NewString()
	targetID := "target-" + uuid.NewString()
	ns := uuid.NewString()

	r.gatedActivity = "gated-" + uuid.NewString()
	r.wfWithPreflight = "wf-with-preflight-" + uuid.NewString()
	r.wfNoPreflight = "wf-no-preflight-" + uuid.NewString()

	policy := fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: activity-requires-test
scopes:
- %s
spec:
  rules:
  - callers:
    - appID: %s
    activities:
    - name: %s
      requires:
      - eventType: activity
        status: Completed
        name: preflight
`, targetID, callerID, r.gatedActivity)

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

func (r *activity) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.caller.WaitUntilRunning(t, ctx)
	r.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()
	targetAppID := r.target.AppID()

	require.NoError(t, callerReg.AddActivityN("preflight", func(ctx task.ActivityContext) (any, error) {
		return "preflight-ok", nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfWithPreflight, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("preflight").Await(nil); err != nil {
			return nil, fmt.Errorf("preflight failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity(r.gatedActivity,
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.gatedActivity, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfNoPreflight, func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity(r.gatedActivity,
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.gatedActivity, err)
		}
		return out, nil
	}))

	require.NoError(t, targetReg.AddActivityN(r.gatedActivity, func(ctx task.ActivityContext) (any, error) {
		r.targetActivityRuns.Add(1)
		return "gated-ok", nil
	}))

	callerClient := client.NewTaskHubGrpcClient(r.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(r.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(r.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, 20*time.Second, 10*time.Millisecond)

	t.Run("allowed when TaskCompleted exists in propagated history", func(t *testing.T) {
		before := r.targetActivityRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfWithPreflight)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"caller should succeed when the required activity completed in history: %v", metadata.GetFailureDetails())
		assert.Equal(t, `"gated-ok"`, metadata.GetOutput().GetValue())
		assert.Equal(t, before+1, r.targetActivityRuns.Load())
	})

	t.Run("denied when no matching TaskCompleted in propagated history", func(t *testing.T) {
		before := r.targetActivityRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfNoPreflight)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails())
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetActivityRuns.Load())
	})
}
