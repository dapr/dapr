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

package suborchestrator

import (
	"context"
	"testing"
	"time"

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
	suite.Register(new(retention))
}

// retention covers the cross-app interaction with the workflow state retention
// policy. Retention is per-instance, per-app: the parent in app0 schedules a
// retention reminder on app0; the cross-app child in app1 schedules its own
// reminder on app1. Each daprd purges its own state when its reminder fires
// — there is no cross-app traversal involved. This test locks that invariant
// in so a future change cannot accidentally turn retention into a recursive
// cross-app operation that would hit the same routing class of bugs as
// recursive terminate / purge.
type retention struct {
	workflow *workflow.Workflow
}

func (r *retention) Setup(t *testing.T) []framework.Option {
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      completed: "1s"
`
	r.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg)),
	)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retention) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const childInstanceID = "test-1-child"

	r.workflow.Registry().AddWorkflowN("MainHelloWorkflow", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		err := wctx.CallChildWorkflow("ChildHelloWorkflow",
			task.WithChildWorkflowAppID(r.workflow.DaprN(1).AppID()),
			task.WithChildWorkflowInstanceID(childInstanceID),
		).Await(&out)
		if err != nil {
			return nil, err
		}
		return out, nil
	})

	r.workflow.RegistryN(1).AddWorkflowN("ChildHelloWorkflow", func(wctx *task.WorkflowContext) (any, error) {
		return "child done", nil
	})

	parent := r.workflow.BackendClient(t, ctx)
	child := r.workflow.BackendClientN(t, ctx, 1)

	parentID, err := parent.ScheduleNewWorkflow(ctx, "MainHelloWorkflow", api.WithInstanceID("test-1"))
	require.NoError(t, err)

	parentMeta, err := parent.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, parentMeta.GetRuntimeStatus())

	childMeta, err := child.WaitForWorkflowCompletion(ctx, api.InstanceID(childInstanceID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, childMeta.GetRuntimeStatus())

	// With a 1s completion-retention policy on both apps, each app's own
	// retention reminder should purge its own instance shortly after the
	// instance reaches COMPLETED. Poll up to 15s to absorb scheduler jitter.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, perr := parent.FetchWorkflowMetadata(ctx, parentID)
		assert.ErrorIs(c, perr, api.ErrInstanceNotFound)
		_, cerr := child.FetchWorkflowMetadata(ctx, api.InstanceID(childInstanceID))
		assert.ErrorIs(c, cerr, api.ErrInstanceNotFound)
	}, time.Second*15, time.Millisecond*100)
}
