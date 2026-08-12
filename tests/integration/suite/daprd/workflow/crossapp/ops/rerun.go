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

package ops

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(rerun))
}

// rerun exercises cross-app rerun: app0's client reruns a completed workflow
// instance hosted on app1 from a specific event via WithRerunAppID.
type rerun struct {
	workflow *workflow.Workflow
}

func (r *rerun) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *rerun) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var acts atomic.Int64
	r.workflow.RegistryN(1).AddWorkflowN("RerunOpsWF", func(wctx *task.WorkflowContext) (any, error) {
		for range 3 {
			if err := wctx.CallActivity("Count").Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	r.workflow.RegistryN(1).AddActivityN("Count", func(task.ActivityContext) (any, error) {
		acts.Add(1)
		return nil, nil
	})

	caller := r.workflow.BackendClient(t, ctx)
	r.workflow.BackendClientN(t, ctx, 1)
	targetAppID := r.workflow.DaprN(1).AppID()
	fetchRemote := []api.FetchWorkflowMetadataOptions{api.WithFetchAppID(targetAppID)}

	id, err := caller.ScheduleNewWorkflow(ctx, "RerunOpsWF",
		api.WithInstanceID("ops-rerun-1"),
		api.WithAppID(targetAppID),
	)
	require.NoError(t, err)
	meta, err := caller.WaitForWorkflowCompletion(ctx, id, fetchRemote...)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	require.Equal(t, int64(3), acts.Load())

	newID, err := caller.RerunWorkflowFromEvent(ctx, id, 2, api.WithRerunAppID(targetAppID))
	require.NoError(t, err)
	require.NotEqual(t, id, newID)

	meta, err = caller.WaitForWorkflowCompletion(ctx, newID, fetchRemote...)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, int64(4), acts.Load(), "rerun from the last activity event must re-execute exactly one activity")

	// Rerun enters the backend through the SDK service rather than the runtime
	// API, so the target app ID must be validated there too: a '.' would
	// otherwise smuggle extra segments into the derived actor type name.
	_, err = caller.RerunWorkflowFromEvent(ctx, id, 2, api.WithRerunAppID(targetAppID+".workflow"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is invalid")
}
