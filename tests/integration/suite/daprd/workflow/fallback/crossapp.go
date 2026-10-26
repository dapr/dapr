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

package fallback

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
	suite.Register(new(crossapp))
}

// crossapp checks that calls from one app reach another app whose daprd is a
// fallback host. App X has a worker, app Y has none. A parent workflow on X
// schedules a child workflow and an activity on Y, and X's client starts a
// workflow on Y and raises an event to it. Y's fallback host accepts all of
// them, and they run once a worker connects to Y.
type crossapp struct {
	workflow *workflow.Workflow
}

func (c *crossapp) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithStagedOperations(),
		withSettle(t, 0),
		withSettle(t, 1),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossapp) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	appY := c.workflow.DaprN(1).AppID()

	require.NoError(t, c.workflow.RegistryN(0).AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		child := ctx.CallChildWorkflow("child",
			task.WithChildWorkflowAppID(appY),
			task.WithChildWorkflowInstanceID("fallback-crossapp-child"),
			task.WithChildWorkflowInput("in"),
		)
		act := ctx.CallActivity("act", task.WithActivityAppID(appY))
		var cout, aout string
		if err := child.Await(&cout); err != nil {
			return nil, err
		}
		if err := act.Await(&aout); err != nil {
			return nil, err
		}
		return cout + "," + aout, nil
	}))
	require.NoError(t, c.workflow.RegistryN(1).AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return "child-" + in, nil
	}))
	require.NoError(t, c.workflow.RegistryN(1).AddActivityN("act", func(task.ActivityContext) (any, error) {
		return "act", nil
	}))
	require.NoError(t, c.workflow.RegistryN(1).AddWorkflowN("remote", func(ctx *task.WorkflowContext) (any, error) {
		var ev string
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(&ev); err != nil {
			return nil, err
		}
		return ev, nil
	}))

	c.workflow.ConnectWorkerN(t, ctx, 0, c.workflow.RegistryN(0))
	c.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)

	// Y's fallback host answers each call to Y at once; without it, each
	// blocks until its deadline. Each such call has a deadline of its own,
	// well below the waits of this test, so a call which blocks fails with
	// DeadlineExceeded. The other calls get a generous deadline of their own.
	const quick = time.Second * 5
	const generous = time.Second * 30

	mgmtX := c.workflow.ManagementClientN(t, ctx, 0)
	mgmtY := c.workflow.ManagementClientN(t, ctx, 1)

	// Y's fallback host registers once the placement tables have shown no
	// executor of app Y for the settle period.
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.True(col, c.workflow.HostsActorType(col, ctx, 1, c.workflow.WorkflowActorType(1)))
	}, generous, time.Millisecond*50)

	// A start and a raise from X's client to a workflow on Y. The start is
	// retried until the placement table which shows Y's host reaches X.
	var remoteID api.InstanceID
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		sctx, cancel := context.WithTimeout(ctx, quick)
		defer cancel()
		var err error
		remoteID, err = mgmtX.ScheduleNewWorkflow(sctx, "remote",
			api.WithInstanceID("fallback-crossapp-remote"),
			api.WithAppID(appY),
		)
		assert.NoError(col, err)
	}, time.Second*20, time.Millisecond*50)

	sctx, cancel := context.WithTimeout(ctx, generous)
	meta, err := mgmtX.FetchWorkflowMetadata(sctx, remoteID, api.WithFetchAppID(appY))
	cancel()
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())

	sctx, cancel = context.WithTimeout(ctx, quick)
	err = mgmtX.RaiseEvent(sctx, remoteID, "go",
		api.WithEventPayload("raised"),
		api.WithRaiseEventAppID(appY),
	)
	cancel()
	require.NoError(t, err)

	// A parent on X whose child workflow and activity run on Y.
	sctx, cancel = context.WithTimeout(ctx, generous)
	parentID, err := mgmtX.ScheduleNewWorkflow(sctx, "parent", api.WithInstanceID("fallback-crossapp-parent"))
	cancel()
	require.NoError(t, err)

	// The child is created on Y's fallback host and waits there.
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		fctx, fcancel := context.WithTimeout(ctx, quick)
		defer fcancel()
		cmeta, cerr := mgmtY.FetchWorkflowMetadata(fctx, "fallback-crossapp-child")
		if assert.NoError(col, cerr) {
			assert.Equal(col, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, cmeta.GetRuntimeStatus())
		}
	}, generous, time.Millisecond*50)

	sctx, cancel = context.WithTimeout(ctx, generous)
	meta, err = mgmtX.FetchWorkflowMetadata(sctx, parentID)
	cancel()
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, meta.GetRuntimeStatus())

	c.workflow.ConnectWorkerN(t, ctx, 1, c.workflow.RegistryN(1))

	meta, err = mgmtX.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"child-in,act"`, meta.GetOutput().GetValue())

	meta, err = mgmtX.WaitForWorkflowCompletion(ctx, remoteID, api.WithFetchAppID(appY))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"raised"`, meta.GetOutput().GetValue())
}
