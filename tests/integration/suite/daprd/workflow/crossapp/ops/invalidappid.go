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
	suite.Register(new(invalidappid))
}

type invalidappid struct {
	workflow *workflow.Workflow
}

func (i *invalidappid) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *invalidappid) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	i.workflow.RegistryN(1).AddWorkflowN("InvalidAppIDWF", func(wctx *task.WorkflowContext) (any, error) {
		var payload string
		if err := wctx.WaitForSingleEvent("Finish", time.Hour).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	caller := i.workflow.BackendClient(t, ctx)
	target := i.workflow.BackendClientN(t, ctx, 1)
	targetAppID := i.workflow.DaprN(1).AppID()

	id, err := caller.ScheduleNewWorkflow(ctx, "InvalidAppIDWF",
		api.WithInstanceID("ops-invalid-appid-1"),
		api.WithAppID(targetAppID),
	)
	require.NoError(t, err)
	_, err = caller.WaitForWorkflowStart(ctx, id, api.WithFetchAppID(targetAppID))
	require.NoError(t, err)

	badAppID := targetAppID + ".workflow"

	fastFail := func(t *testing.T, op func(context.Context) error) {
		t.Helper()
		opctx, cancel := context.WithTimeout(ctx, time.Second*20)
		defer cancel()
		operr := op(opctx)
		require.Error(t, operr)
		assert.Contains(t, operr.Error(), "is invalid")
		require.NoError(t, opctx.Err(), "operation must fail fast on validation, not by exhausting the timeout")
	}

	t.Run("schedule", func(t *testing.T) {
		fastFail(t, func(opctx context.Context) error {
			_, serr := caller.ScheduleNewWorkflow(opctx, "InvalidAppIDWF",
				api.WithInstanceID("ops-invalid-appid-schedule"),
				api.WithAppID(badAppID),
			)
			return serr
		})

		_, err = target.FetchWorkflowMetadata(ctx, "ops-invalid-appid-schedule")
		require.ErrorIs(t, err, api.ErrInstanceNotFound)
		_, err = caller.FetchWorkflowMetadata(ctx, "ops-invalid-appid-schedule")
		require.ErrorIs(t, err, api.ErrInstanceNotFound)
	})

	t.Run("fetch metadata", func(t *testing.T) {
		fastFail(t, func(opctx context.Context) error {
			_, ferr := caller.FetchWorkflowMetadata(opctx, id, api.WithFetchAppID(badAppID))
			return ferr
		})
	})

	t.Run("watch runtime status", func(t *testing.T) {
		sidecar := protos.NewTaskHubSidecarServiceClient(i.workflow.Dapr().GRPCConn(t, ctx))
		fastFail(t, func(opctx context.Context) error {
			_, werr := sidecar.WaitForInstanceStart(opctx, &protos.GetInstanceRequest{
				InstanceId: string(id),
				Router:     &protos.TaskRouter{TargetAppID: &badAppID},
			})
			return werr
		})
	})

	t.Run("raise event", func(t *testing.T) {
		fastFail(t, func(opctx context.Context) error {
			return caller.RaiseEvent(opctx, id, "Finish",
				api.WithEventPayload("nope"),
				api.WithRaiseEventAppID(badAppID),
			)
		})
	})

	t.Run("suspend", func(t *testing.T) {
		fastFail(t, func(opctx context.Context) error {
			return caller.SuspendWorkflow(opctx, id, "pause it", api.WithSuspendAppID(badAppID))
		})
	})

	t.Run("resume", func(t *testing.T) {
		fastFail(t, func(opctx context.Context) error {
			return caller.ResumeWorkflow(opctx, id, "resume it", api.WithResumeAppID(badAppID))
		})
	})

	t.Run("terminate", func(t *testing.T) {
		fastFail(t, func(opctx context.Context) error {
			return caller.TerminateWorkflow(opctx, id, api.WithTerminateAppID(badAppID))
		})
	})

	t.Run("purge", func(t *testing.T) {
		fastFail(t, func(opctx context.Context) error {
			return caller.PurgeWorkflowState(opctx, id, api.WithPurgeAppID(badAppID))
		})
	})

	t.Run("rejected character set", func(t *testing.T) {
		for _, appID := range []string{
			"app.with.dots",
			"app with spaces",
			"app/slash",
			"app:colon",
			"app*star",
			"..",
		} {
			t.Run(appID, func(t *testing.T) {
				fastFail(t, func(opctx context.Context) error {
					_, serr := caller.ScheduleNewWorkflow(opctx, "InvalidAppIDWF",
						api.WithInstanceID("ops-invalid-appid-charset"),
						api.WithAppID(appID),
					)
					return serr
				})
			})
		}
	})

	meta, err := caller.FetchWorkflowMetadata(ctx, id, api.WithFetchAppID(targetAppID))
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus())

	require.NoError(t, caller.RaiseEvent(ctx, id, "Finish",
		api.WithEventPayload("done"),
		api.WithRaiseEventAppID(targetAppID),
	))
	meta, err = caller.WaitForWorkflowCompletion(ctx, id, api.WithFetchAppID(targetAppID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
}
