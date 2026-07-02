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
	suite.Register(new(crossapp))
}

// Multi-requires + AppID-filter coverage for cross-app activity invocations.
type crossapp struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd

	targetActivityRuns atomic.Int64

	processPayment            string
	processPaymentAppGated    string
	processPaymentAppMismatch string
	logReceipt                string
	wfFullHistory             string
	wfPartialHistory          string
	wfNoPropagation           string
	wfAppIDMatch              string
	wfAppIDMismatch           string
	wfLogReceipt              string
}

func (r *crossapp) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)

	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	callerID := "caller-" + uuid.NewString()
	targetID := "target-" + uuid.NewString()
	ns := uuid.NewString()

	r.processPayment = "process-payment"
	r.processPaymentAppGated = "process-payment-app-gated"
	r.processPaymentAppMismatch = "process-payment-app-mismatch"
	r.logReceipt = "log-receipt"
	r.wfFullHistory = "wf-full"
	r.wfPartialHistory = "wf-partial"
	r.wfNoPropagation = "wf-no-prop"
	r.wfAppIDMatch = "wf-appid-match"
	r.wfAppIDMismatch = "wf-appid-mismatch"
	r.wfLogReceipt = "wf-log-receipt"

	policy := fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: requires-test
scopes:
- %s
spec:
  rules:
  - callers:
    - appID: %s
    activities:
    - name: %s
      requires:
      - eventType: activity.completed
        name: fraud-check
        appID: %s
      - eventType: activity.completed
        name: human-approval
        appID: %s
    - name: %s
      requires:
      - eventType: activity.completed
        name: fraud-check
        appID: %s
    - name: %s
      requires:
      - eventType: activity.completed
        name: fraud-check
        appID: nonexistent-app
    - name: %s
`, targetID, callerID,
		r.processPayment, callerID, callerID,
		r.processPaymentAppGated, callerID,
		r.processPaymentAppMismatch,
		r.logReceipt)

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

func (r *crossapp) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.sched.WaitUntilRunning(t, ctx)
	r.caller.WaitUntilRunning(t, ctx)
	r.target.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	targetReg := task.NewTaskRegistry()
	targetAppID := r.target.AppID()

	require.NoError(t, callerReg.AddWorkflowN(r.wfFullHistory, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		if err := ctx.CallActivity("human-approval").Await(nil); err != nil {
			return nil, fmt.Errorf("human-approval failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity(r.processPayment,
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.processPayment, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfPartialHistory, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity(r.processPayment,
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.processPayment, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfNoPropagation, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		if err := ctx.CallActivity("human-approval").Await(nil); err != nil {
			return nil, fmt.Errorf("human-approval failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity(r.processPayment,
			task.WithActivityAppID(targetAppID),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.processPayment, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfAppIDMatch, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity(r.processPaymentAppGated,
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.processPaymentAppGated, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfAppIDMismatch, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity(r.processPaymentAppMismatch,
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.processPaymentAppMismatch, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddWorkflowN(r.wfLogReceipt, func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity(r.logReceipt,
			task.WithActivityAppID(targetAppID),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.logReceipt, err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddActivityN("fraud-check", func(ctx task.ActivityContext) (any, error) {
		return "fraud-ok", nil
	}))
	require.NoError(t, callerReg.AddActivityN("human-approval", func(ctx task.ActivityContext) (any, error) {
		return "approved", nil
	}))

	require.NoError(t, targetReg.AddActivityN(r.processPayment, func(ctx task.ActivityContext) (any, error) {
		r.targetActivityRuns.Add(1)
		return "payment-processed", nil
	}))
	require.NoError(t, targetReg.AddActivityN(r.processPaymentAppGated, func(ctx task.ActivityContext) (any, error) {
		r.targetActivityRuns.Add(1)
		return "payment-app-gated", nil
	}))
	require.NoError(t, targetReg.AddActivityN(r.processPaymentAppMismatch, func(ctx task.ActivityContext) (any, error) {
		r.targetActivityRuns.Add(1)
		return "payment-app-mismatch", nil
	}))
	require.NoError(t, targetReg.AddActivityN(r.logReceipt, func(ctx task.ActivityContext) (any, error) {
		return "logged", nil
	}))

	callerClient := client.NewTaskHubGrpcClient(r.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(r.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(r.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(r.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, 20*time.Second, 10*time.Millisecond)

	t.Run("allowed when both required activities are present in propagated history", func(t *testing.T) {
		before := r.targetActivityRuns.Load()

		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfFullHistory)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata),
			"workflow should complete when requires are satisfied")
		assert.Nil(t, metadata.GetFailureDetails(),
			"caller should succeed: %v", metadata.GetFailureDetails())
		assert.Equal(t, `"payment-processed"`, metadata.GetOutput().GetValue())
		assert.Equal(t, before+1, r.targetActivityRuns.Load(),
			"gated activity should have run exactly once on target")
	})

	t.Run("denied when one required activity is missing from history", func(t *testing.T) {
		before := r.targetActivityRuns.Load()

		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfPartialHistory)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when a required activity is missing from propagated history")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetActivityRuns.Load(),
			"gated activity must NOT execute on the target when access is denied")
	})

	t.Run("denied when caller does not propagate history", func(t *testing.T) {
		before := r.targetActivityRuns.Load()

		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfNoPropagation)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when caller does not propagate history")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetActivityRuns.Load(),
			"gated activity must NOT execute on the target when access is denied")
	})

	t.Run("rule without requires is unaffected by absence of history", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfLogReceipt)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"log-receipt workflow should succeed: %v", metadata.GetFailureDetails())
		assert.Equal(t, `"logged"`, metadata.GetOutput().GetValue())
	})

	t.Run("allowed when requires.appID matches the producing chunk's appID", func(t *testing.T) {
		before := r.targetActivityRuns.Load()

		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfAppIDMatch)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"caller should succeed when chunk appID matches the requires entry: %v",
			metadata.GetFailureDetails())
		assert.Equal(t, `"payment-app-gated"`, metadata.GetOutput().GetValue())
		assert.Equal(t, before+1, r.targetActivityRuns.Load(),
			"appID-gated activity should have run exactly once on target")
	})

	t.Run("denied when requires.appID points to a different app than the chunk", func(t *testing.T) {
		before := r.targetActivityRuns.Load()

		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfAppIDMismatch)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when requires.appID doesn't match the producing chunk")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetActivityRuns.Load(),
			"appID-mismatch activity must NOT execute on the target when appID filter excludes the chunk")
	})
}
