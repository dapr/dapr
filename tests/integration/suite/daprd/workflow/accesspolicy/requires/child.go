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
	suite.Register(new(child))
}

// requires gating cross-app child workflow scheduling on a prior
// ChildWorkflowInstanceCompleted in the caller's propagated history.
type child struct {
	workflow *workflow.Workflow

	callerID string
	targetID string

	targetWorkflowRuns atomic.Int64

	sensitiveChildWF string
	publicChildWF    string
	wfFullHistory    string
	wfNoHistory      string
	wfNoPropagation  string
	wfPublic         string
}

func (r *child) Setup(t *testing.T) []framework.Option {
	r.callerID = "caller-" + uuid.NewString()
	r.targetID = "target-" + uuid.NewString()

	r.sensitiveChildWF = "sensitive-child"
	r.publicChildWF = "public-child"
	r.wfFullHistory = "wf-full-history"
	r.wfNoHistory = "wf-no-history"
	r.wfNoPropagation = "wf-no-propagation"
	r.wfPublic = "wf-public"

	policy := fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: child-requires-test
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
      - eventType: workflow.completed
        name: preflight-wf
        appID: %s
    - name: %s
      operations:
      - schedule
`, r.targetID, r.callerID, r.sensitiveChildWF, r.callerID, r.publicChildWF)

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

func (r *child) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	caller := r.workflow.RegistryN(0)
	target := r.workflow.RegistryN(1)

	require.NoError(t, caller.AddWorkflowN("preflight-wf", func(ctx *task.WorkflowContext) (any, error) {
		return "preflight-ok", nil
	}))

	require.NoError(t, caller.AddWorkflowN(r.wfFullHistory, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallChildWorkflow("preflight-wf").Await(nil); err != nil {
			return nil, fmt.Errorf("preflight-wf failed: %w", err)
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

	require.NoError(t, caller.AddWorkflowN(r.wfNoHistory, func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(r.targetID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, caller.AddWorkflowN(r.wfNoPropagation, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallChildWorkflow("preflight-wf").Await(nil); err != nil {
			return nil, fmt.Errorf("preflight-wf failed: %w", err)
		}
		var out string
		if err := ctx.CallChildWorkflow(r.sensitiveChildWF,
			task.WithChildWorkflowAppID(r.targetID),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.sensitiveChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, caller.AddWorkflowN(r.wfPublic, func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow(r.publicChildWF,
			task.WithChildWorkflowAppID(r.targetID),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", r.publicChildWF, err)
		}
		return out, nil
	}))

	require.NoError(t, target.AddWorkflowN(r.sensitiveChildWF, func(ctx *task.WorkflowContext) (any, error) {
		r.targetWorkflowRuns.Add(1)
		return "sensitive-child-completed", nil
	}))
	require.NoError(t, target.AddWorkflowN(r.publicChildWF, func(ctx *task.WorkflowContext) (any, error) {
		return "public-child-completed", nil
	}))

	callerClient := r.workflow.BackendClientN(t, ctx, 0)
	r.workflow.BackendClientN(t, ctx, 1)

	t.Run("child workflow allowed when ChildWorkflowInstanceCompleted in propagated history", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfFullHistory)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"caller should succeed when the required child wf completed in history: %v", metadata.GetFailureDetails())
		assert.Equal(t, `"sensitive-child-completed"`, metadata.GetOutput().GetValue())
		assert.Equal(t, before+1, r.targetWorkflowRuns.Load(),
			"sensitive child WF body should have run exactly once on target")
	})

	t.Run("child workflow denied when no matching ChildWorkflowInstanceCompleted in propagated history", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfNoHistory)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when the required child wf completion is missing from propagated history")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetWorkflowRuns.Load(),
			"sensitive child WF must NOT execute on target when access is denied")
	})

	t.Run("child workflow denied when caller does not propagate history", func(t *testing.T) {
		before := r.targetWorkflowRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfNoPropagation)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"workflow should fail when caller does not propagate history")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetWorkflowRuns.Load(),
			"sensitive child WF must NOT execute on target when access is denied")
	})

	t.Run("rule without requires is unaffected by absence of history", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfPublic)
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"public child WF should succeed: %v", metadata.GetFailureDetails())
		assert.Equal(t, `"public-child-completed"`, metadata.GetOutput().GetValue())
	})
}
