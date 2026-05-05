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

// crossapp verifies that WorkflowAccessPolicy rules with a
// `requires` block gate cross-app activity invocations on the contents of
// the caller's propagated history. The target app exposes a sensitive
// `ProcessPayment` activity that may only run when the caller has
// previously completed `FraudCheckPassed` AND `HumanApprovalReceived`
// activities AND propagates that history along with the call.
//
// verify the following for same-app callers:
//   - history satisfies both requirements and is propagated  = allowed
//   - history is missing one of the required completions     = denied
//   - history is complete but the caller does not propagate  = denied
//   - sibling activity with no `requires` block still works as before
type crossapp struct {
	sentry *sentry.Sentry
	place  *placement.Placement
	sched  *scheduler.Scheduler
	db     *sqlite.SQLite
	caller *daprd.Daprd
	target *daprd.Daprd

	targetActivityRuns atomic.Int64
}

func (r *crossapp) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t)

	r.place = placement.New(t, placement.WithSentry(t, r.sentry))
	r.sched = scheduler.New(t, scheduler.WithSentry(r.sentry), scheduler.WithID("dapr-scheduler-server-0"))
	r.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfaclrequiresconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true
`), 0o600))

	// `ProcessPayment` only runs when both required ActivityCompleted events
	// (FraudCheckPassed + HumanApprovalReceived) appear in the caller's
	// propagated history. `LogReceipt` has no requires block
	policy := []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: requires-test
scopes:
- wfacl-requires-target
spec:
  defaultAction: deny
  rules:
  - callers:
    - appID: wfacl-requires-caller
    operations:
    - type: activity
      name: ProcessPayment
      action: allow
      requires:
      - status: Completed
        activityName: FraudCheckPassed
      - status: Completed
        activityName: HumanApprovalReceived
    - type: activity
      name: ProcessPaymentAppGated
      action: allow
      requires:
      - status: Completed
        activityName: FraudCheckPassed
        appID: wfacl-requires-caller
    - type: activity
      name: ProcessPaymentAppMismatch
      action: allow
      requires:
      - status: Completed
        activityName: FraudCheckPassed
        appID: nonexistent-app
    - type: activity
      name: LogReceipt
      action: allow
  - callers:
    - appID: wfacl-requires-target
    operations:
    - type: activity
      name: "*"
      action: allow
`)

	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	r.caller = daprd.New(t,
		daprd.WithAppID("wfacl-requires-caller"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(r.db.GetComponent(t)),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithSentry(t, r.sentry),
	)
	r.target = daprd.New(t,
		daprd.WithAppID("wfacl-requires-target"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
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

	// `WF_FullHistory` runs FraudCheckPassed + HumanApprovalReceived locally
	// then calls ProcessPayment on the target with PropagateOwnHistory.
	require.NoError(t, callerReg.AddWorkflowN("WF_FullHistory", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("FraudCheckPassed").Await(nil); err != nil {
			return nil, fmt.Errorf("FraudCheckPassed failed: %w", err)
		}
		if err := ctx.CallActivity("HumanApprovalReceived").Await(nil); err != nil {
			return nil, fmt.Errorf("HumanApprovalReceived failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity("ProcessPayment",
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("ProcessPayment failed: %w", err)
		}
		return out, nil
	}))

	// `WF_PartialHistory` runs only FraudCheckPassed (skips approval) and
	// propagates that partial history to ProcessPayment. The target's
	// requires block is not satisfied = access should be denied.
	require.NoError(t, callerReg.AddWorkflowN("WF_PartialHistory", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("FraudCheckPassed").Await(nil); err != nil {
			return nil, fmt.Errorf("FraudCheckPassed failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity("ProcessPayment",
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("ProcessPayment failed: %w", err)
		}
		return out, nil
	}))

	// `WF_NoPropagation` runs all the prereq activities but does NOT
	// pass PropagateOwnHistory. The target therefore sees no propagated
	// history = access should be denied even though the caller's local
	// history contains the required events.
	require.NoError(t, callerReg.AddWorkflowN("WF_NoPropagation", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("FraudCheckPassed").Await(nil); err != nil {
			return nil, fmt.Errorf("FraudCheckPassed failed: %w", err)
		}
		if err := ctx.CallActivity("HumanApprovalReceived").Await(nil); err != nil {
			return nil, fmt.Errorf("HumanApprovalReceived failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity("ProcessPayment",
			task.WithActivityAppID(targetAppID),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("ProcessPayment failed: %w", err)
		}
		return out, nil
	}))

	// `WF_AppIDMatch` runs FraudCheckPassed then calls
	// ProcessPaymentAppGated. The target's rule requires appID matching the
	// caller — the propagated chunk's appID is wfacl-requires-caller, which
	// matches = allowed.
	require.NoError(t, callerReg.AddWorkflowN("WF_AppIDMatch", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("FraudCheckPassed").Await(nil); err != nil {
			return nil, fmt.Errorf("FraudCheckPassed failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity("ProcessPaymentAppGated",
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("ProcessPaymentAppGated failed: %w", err)
		}
		return out, nil
	}))

	// `WF_AppIDMismatch` runs FraudCheckPassed then calls
	// ProcessPaymentAppMismatch. The target's rule pins appID to a
	// non-existent app = chunk appID never matches = denied.
	require.NoError(t, callerReg.AddWorkflowN("WF_AppIDMismatch", func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("FraudCheckPassed").Await(nil); err != nil {
			return nil, fmt.Errorf("FraudCheckPassed failed: %w", err)
		}
		var out string
		if err := ctx.CallActivity("ProcessPaymentAppMismatch",
			task.WithActivityAppID(targetAppID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("ProcessPaymentAppMismatch failed: %w", err)
		}
		return out, nil
	}))

	// `WF_LogReceipt` invokes a sibling activity `LogReceipt` that has no
	// requires block
	require.NoError(t, callerReg.AddWorkflowN("WF_LogReceipt", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("LogReceipt",
			task.WithActivityAppID(targetAppID),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("LogReceipt failed: %w", err)
		}
		return out, nil
	}))

	require.NoError(t, callerReg.AddActivityN("FraudCheckPassed", func(ctx task.ActivityContext) (any, error) {
		return "fraud-ok", nil
	}))
	require.NoError(t, callerReg.AddActivityN("HumanApprovalReceived", func(ctx task.ActivityContext) (any, error) {
		return "approved", nil
	}))

	require.NoError(t, targetReg.AddActivityN("ProcessPayment", func(ctx task.ActivityContext) (any, error) {
		r.targetActivityRuns.Add(1)
		return "payment-processed", nil
	}))
	require.NoError(t, targetReg.AddActivityN("ProcessPaymentAppGated", func(ctx task.ActivityContext) (any, error) {
		r.targetActivityRuns.Add(1)
		return "payment-app-gated", nil
	}))
	require.NoError(t, targetReg.AddActivityN("ProcessPaymentAppMismatch", func(ctx task.ActivityContext) (any, error) {
		r.targetActivityRuns.Add(1)
		return "payment-app-mismatch", nil
	}))
	require.NoError(t, targetReg.AddActivityN("LogReceipt", func(ctx task.ActivityContext) (any, error) {
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

		id, err := callerClient.ScheduleNewWorkflow(ctx, "WF_FullHistory")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata),
			"workflow should complete when requires are satisfied")
		assert.Nil(t, metadata.GetFailureDetails(),
			"WF_FullHistory should succeed: %v", metadata.GetFailureDetails())
		assert.Equal(t, `"payment-processed"`, metadata.GetOutput().GetValue())
		assert.Equal(t, before+1, r.targetActivityRuns.Load(),
			"ProcessPayment activity body should have run exactly once on target")
	})

	t.Run("denied when one required activity is missing from history", func(t *testing.T) {
		before := r.targetActivityRuns.Load()

		id, err := callerClient.ScheduleNewWorkflow(ctx, "WF_PartialHistory")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when a required activity is missing from propagated history")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"required history not satisfied",
			"requires-driven denials must surface a distinct error message so workflow authors can branch on the cause")
		assert.Equal(t, before, r.targetActivityRuns.Load(),
			"ProcessPayment must NOT execute on the target when access is denied")
	})

	t.Run("denied when caller does not propagate history", func(t *testing.T) {
		before := r.targetActivityRuns.Load()

		id, err := callerClient.ScheduleNewWorkflow(ctx, "WF_NoPropagation")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when caller does not propagate history")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"required history not satisfied",
			"no propagated history means requires can't be satisfied = distinct deny reason")
		assert.Equal(t, before, r.targetActivityRuns.Load(),
			"ProcessPayment must NOT execute on the target when access is denied")
	})

	t.Run("rule without requires is unaffected by absence of history", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "WF_LogReceipt")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"WF_LogReceipt should succeed: %v", metadata.GetFailureDetails())
		assert.Equal(t, `"logged"`, metadata.GetOutput().GetValue())
	})

	t.Run("allowed when requires.appID matches the producing chunk's appID", func(t *testing.T) {
		before := r.targetActivityRuns.Load()

		id, err := callerClient.ScheduleNewWorkflow(ctx, "WF_AppIDMatch")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"WF_AppIDMatch should succeed when chunk appID matches the requires entry: %v",
			metadata.GetFailureDetails())
		assert.Equal(t, `"payment-app-gated"`, metadata.GetOutput().GetValue())
		assert.Equal(t, before+1, r.targetActivityRuns.Load(),
			"ProcessPaymentAppGated should have run exactly once on target")
	})

	t.Run("denied when requires.appID points to a different app than the chunk", func(t *testing.T) {
		before := r.targetActivityRuns.Load()

		id, err := callerClient.ScheduleNewWorkflow(ctx, "WF_AppIDMismatch")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when requires.appID doesn't match the producing chunk")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"required history not satisfied",
			"appID-filter mismatch is a requires-unmet denial")
		assert.Equal(t, before, r.targetActivityRuns.Load(),
			"ProcessPaymentAppMismatch must NOT execute on the target when appID filter excludes the chunk")
	})
}
