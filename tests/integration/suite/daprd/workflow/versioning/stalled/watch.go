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

package stalled

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(watch))
}

// watch verifies a completion watch opened while a workflow is stalled
// resolves once the stall lifts: the stalled actor serves the stored status
// snapshot on registration and parks the watcher until recovery, instead of
// failing the watch with a stalled-actor error.
type watch struct {
	workflow *workflow.Workflow
}

func (w *watch) Setup(t *testing.T) []framework.Option {
	w.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *watch) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	w.workflow.Registry().AddVersionedWorkflowN("workflow", "v1", true, func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("Continue", -1).Await(nil)
	})

	clientCtx, cancelClient := context.WithCancel(ctx)
	cl := w.workflow.BackendClient(t, clientCtx)
	id, err := cl.ScheduleNewWorkflow(ctx, "workflow")
	require.NoError(t, err)

	wf.WaitForWorkflowStartedEvent(t, ctx, cl, id)

	w.workflow.ResetRegistry(t)
	cancelClient()
	w.workflow.WaitForNoConnectedWorkers(t, ctx)

	w.workflow.Registry().AddVersionedWorkflowN("workflow", "v2", true, func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("Continue", -1).Await(nil)
	})
	clientCtx, cancelClient = context.WithCancel(ctx)
	cl = w.workflow.BackendClient(t, clientCtx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, cl.RaiseEvent(ctx, id, "Continue"))
	}, time.Second*20, time.Millisecond*10)

	// Dialed before the stall so the watch lands while the stalling turn still
	// parks in the hold (which owns the actor turn lock). A worker-less client
	// keeps the v2 version from being advertised while v1 recovers below.
	watcher := client.NewTaskHubGrpcClient(w.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))

	wf.WaitForRuntimeStatus(t, ctx, cl, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	type outcome struct {
		md  *protos.WorkflowMetadata
		err error
	}
	watched := make(chan outcome, 1)
	go func() {
		md, werr := watcher.WaitForWorkflowCompletion(ctx, id)
		watched <- outcome{md, werr}
	}()

	select {
	case res := <-watched:
		require.NoError(t, res.err, "a watch opened during the stall must park, not fail")
		require.Fail(t, "the watch cannot resolve while the workflow is still stalled")
	case <-time.After(time.Second * 2):
	}

	w.workflow.ResetRegistry(t)
	cancelClient()
	w.workflow.WaitForNoConnectedWorkers(t, ctx)

	w.workflow.Registry().AddVersionedWorkflowN("workflow", "v1", true, func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("Continue", -1).Await(nil)
	})
	clientCtx, cancelClient = context.WithCancel(ctx)
	t.Cleanup(cancelClient)
	w.workflow.BackendClient(t, clientCtx)

	select {
	case res := <-watched:
		require.NoError(t, res.err)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED.String(), res.md.GetRuntimeStatus().String())
	case <-time.After(time.Second * 30):
		require.Fail(t, "watch opened during the stall never resolved after recovery")
	}
}
