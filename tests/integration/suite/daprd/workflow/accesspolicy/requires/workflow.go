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
	suite.Register(new(workflowstarted))
}

// requires matching on eventType=workflow.started (a child-workflow creation
// in the caller's propagated history).
type workflowstarted struct {
	workflow *workflow.Workflow

	callerID string
	targetID string

	targetActivityRuns atomic.Int64

	childWFName  string
	wfWithChild  string
	wfNoChild    string
	activityName string
}

func (r *workflowstarted) Setup(t *testing.T) []framework.Option {
	r.callerID = "caller-" + uuid.NewString()
	r.targetID = "target-" + uuid.NewString()

	r.childWFName = "required-child-wf"
	r.wfWithChild = "wf-with-child"
	r.wfNoChild = "wf-no-child"
	r.activityName = "gated-activity"

	policy := fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: workflow-requires-test
scopes:
- %s
spec:
  rules:
  - callers:
    - appID: %s
    activities:
    - name: %s
      requires:
      - eventType: workflow.started
        name: %s
        appID: %s
`, r.targetID, r.callerID, r.activityName, r.childWFName, r.callerID)

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

func (r *workflowstarted) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	caller := r.workflow.RegistryN(0)
	target := r.workflow.RegistryN(1)
	activityName := r.activityName
	childWFName := r.childWFName

	require.NoError(t, caller.AddWorkflowN(childWFName, func(ctx *task.WorkflowContext) (any, error) {
		return "child-ok", nil
	}))

	// Creates the required child workflow before calling the gated activity.
	require.NoError(t, caller.AddWorkflowN(r.wfWithChild, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CallChildWorkflow(childWFName).Await(nil); err != nil {
			return nil, fmt.Errorf("%s failed: %w", childWFName, err)
		}
		var out string
		if err := ctx.CallActivity(activityName,
			task.WithActivityAppID(r.targetID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", activityName, err)
		}
		return out, nil
	}))

	require.NoError(t, caller.AddWorkflowN(r.wfNoChild, func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity(activityName,
			task.WithActivityAppID(r.targetID),
			task.WithHistoryPropagation(api.PropagateOwnHistory()),
		).Await(&out); err != nil {
			return nil, fmt.Errorf("%s failed: %w", activityName, err)
		}
		return out, nil
	}))

	require.NoError(t, target.AddActivityN(activityName, func(ctx task.ActivityContext) (any, error) {
		r.targetActivityRuns.Add(1)
		return "gated-ok", nil
	}))

	callerClient := r.workflow.BackendClientN(t, ctx, 0)
	r.workflow.BackendClientN(t, ctx, 1)

	t.Run("allowed when caller created the required child workflow", func(t *testing.T) {
		before := r.targetActivityRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfWithChild)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"caller should succeed when it created the required child workflow: %v", metadata.GetFailureDetails())
		assert.Equal(t, `"gated-ok"`, metadata.GetOutput().GetValue())
		assert.Equal(t, before+1, r.targetActivityRuns.Load())
	})

	t.Run("denied when caller did not create the required child workflow", func(t *testing.T) {
		before := r.targetActivityRuns.Load()
		id, err := callerClient.ScheduleNewWorkflow(ctx, r.wfNoChild)
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails())
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(),
			"access denied by workflow access policy")
		assert.Equal(t, before, r.targetActivityRuns.Load())
	})
}
