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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(requireboth))
}

// Multiple items inside a single `requires` list compose as AND and must
// appear in the listed order: every listed event must be present in propagated
// history, in the order the entries are written.
type requireboth struct {
	workflow *workflow.Workflow

	callerID string
	targetID string

	targetWorkflowRuns atomic.Int64

	sensitiveChildWF string
	wfBoth           string
	wfReversed       string
	wfOnlyFraud      string
	wfOnlyApproval   string
	wfNeither        string
}

func (r *requireboth) Setup(t *testing.T) []framework.Option {
	r.callerID = "caller-" + uuid.NewString()
	r.targetID = "target-" + uuid.NewString()

	r.sensitiveChildWF = "sensitive-child"
	r.wfBoth = "wf-both"
	r.wfReversed = "wf-reversed"
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
      - schedule
      requires:
      - eventType: activity.completed
        name: fraud-check
        appID: %s
      - eventType: activity.completed
        name: human-approval
        appID: %s
`, r.targetID, r.callerID, r.sensitiveChildWF, r.callerID, r.callerID)

	targetResDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), policy, 0o600))

	r.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithMTLS(t),
		workflow.WithDaprdOptions(0, daprd.WithAppID(r.callerID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(r.targetID), daprd.WithResourcesDir(targetResDir)),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *requireboth) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	caller := r.workflow.RegistryN(0)
	target := r.workflow.RegistryN(1)

	require.NoError(t, caller.AddActivityN("fraud-check", func(ctx task.ActivityContext) (any, error) {
		return "fraud-ok", nil
	}))
	require.NoError(t, caller.AddActivityN("human-approval", func(ctx task.ActivityContext) (any, error) {
		return "approved", nil
	}))

	require.NoError(t, caller.AddWorkflowN(r.wfBoth, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		if err := ctx.CallActivity("human-approval").Await(nil); err != nil {
			return nil, fmt.Errorf("human-approval failed: %w", err)
		}
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(r.targetID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	// Completes both prerequisites but in the reverse of the policy's order
	// (human-approval before fraud-check), so the ordered match must deny.
	require.NoError(t, caller.AddWorkflowN(r.wfReversed, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("human-approval").Await(nil); err != nil {
			return nil, fmt.Errorf("human-approval failed: %w", err)
		}
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(r.targetID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, caller.AddWorkflowN(r.wfOnlyFraud, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("fraud-check").Await(nil); err != nil {
			return nil, fmt.Errorf("fraud-check failed: %w", err)
		}
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(r.targetID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, caller.AddWorkflowN(r.wfOnlyApproval, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("human-approval").Await(nil); err != nil {
			return nil, fmt.Errorf("human-approval failed: %w", err)
		}
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(r.targetID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, caller.AddWorkflowN(r.wfNeither, func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(r.targetID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, target.AddWorkflowN(r.sensitiveChildWF, func(ctx *task.WorkflowContext) (any, error) {
		r.targetWorkflowRuns.Add(1)
		return "sensitive-completed", nil
	}))

	callerClient := r.workflow.BackendClientN(t, ctx, 0)
	r.workflow.BackendClientN(t, ctx, 1)

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

	t.Run("denied when both present but completed out of order", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfReversed)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when the required activities complete in the wrong order")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetWorkflowRuns.Load(),
			"sensitive child WF must NOT execute when requires are satisfied out of order")
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
