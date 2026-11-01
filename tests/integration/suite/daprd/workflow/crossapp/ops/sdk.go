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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(sdk))
}

// sdk exercises every client-level workflow operation cross-app through the
// durabletask SDK client: app0's client targets workflows hosted on app1 via
// the With*AppID options.
type sdk struct {
	workflow *workflow.Workflow
}

func (s *sdk) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *sdk) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.RegistryN(1).AddWorkflowN("OpsWF", func(wctx *task.WorkflowContext) (any, error) {
		var payload string
		if err := wctx.WaitForSingleEvent("Finish", time.Hour).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	caller := s.workflow.BackendClient(t, ctx)
	target := s.workflow.BackendClientN(t, ctx, 1)
	targetAppID := s.workflow.DaprN(1).AppID()

	fetchRemote := []api.FetchWorkflowMetadataOptions{api.WithFetchAppID(targetAppID)}

	t.Run("schedule get raise complete", func(t *testing.T) {
		id, err := caller.ScheduleNewWorkflow(ctx, "OpsWF",
			api.WithInstanceID("ops-sdk-1"),
			api.WithAppID(targetAppID),
		)
		require.NoError(t, err)

		_, err = caller.WaitForWorkflowStart(ctx, id, fetchRemote...)
		require.NoError(t, err)

		// The instance lives on the target app, not the caller.
		meta, err := target.FetchWorkflowMetadata(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
		_, err = caller.FetchWorkflowMetadata(ctx, id)
		require.ErrorIs(t, err, api.ErrInstanceNotFound)

		// Cross-app get.
		meta, err = caller.FetchWorkflowMetadata(ctx, id, fetchRemote...)
		require.NoError(t, err)
		assert.Equal(t, "OpsWF", meta.GetName())
		assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())

		// Cross-app raise event completes the workflow.
		require.NoError(t, caller.RaiseEvent(ctx, id, "Finish",
			api.WithEventPayload("done-payload"),
			api.WithRaiseEventAppID(targetAppID),
		))
		meta, err = caller.WaitForWorkflowCompletion(ctx, id, fetchRemote...)
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Equal(t, `"done-payload"`, meta.GetOutput().GetValue())
	})

	t.Run("suspend and resume", func(t *testing.T) {
		id, err := caller.ScheduleNewWorkflow(ctx, "OpsWF",
			api.WithInstanceID("ops-sdk-2"),
			api.WithAppID(targetAppID),
		)
		require.NoError(t, err)
		_, err = caller.WaitForWorkflowStart(ctx, id, fetchRemote...)
		require.NoError(t, err)

		require.NoError(t, caller.SuspendWorkflow(ctx, id, "pause it", api.WithSuspendAppID(targetAppID)))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var meta *protos.WorkflowMetadata
			meta, err = target.FetchWorkflowMetadata(ctx, id)
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, api.RUNTIME_STATUS_SUSPENDED, meta.GetRuntimeStatus())
		}, time.Second*30, time.Millisecond*10)

		require.NoError(t, caller.ResumeWorkflow(ctx, id, "resume it", api.WithResumeAppID(targetAppID)))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var meta *protos.WorkflowMetadata
			meta, err = target.FetchWorkflowMetadata(ctx, id)
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())
		}, time.Second*30, time.Millisecond*10)
	})

	t.Run("terminate and purge", func(t *testing.T) {
		id, err := caller.ScheduleNewWorkflow(ctx, "OpsWF",
			api.WithInstanceID("ops-sdk-3"),
			api.WithAppID(targetAppID),
		)
		require.NoError(t, err)
		_, err = caller.WaitForWorkflowStart(ctx, id, fetchRemote...)
		require.NoError(t, err)

		require.NoError(t, caller.TerminateWorkflow(ctx, id, api.WithTerminateAppID(targetAppID)))
		meta, err := caller.WaitForWorkflowCompletion(ctx, id, fetchRemote...)
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_TERMINATED, meta.GetRuntimeStatus())

		require.NoError(t, caller.PurgeWorkflowState(ctx, id, api.WithPurgeAppID(targetAppID)))
		_, err = target.FetchWorkflowMetadata(ctx, id)
		require.ErrorIs(t, err, api.ErrInstanceNotFound)
	})
}
