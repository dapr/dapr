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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(hostdeath))
}

// hostdeath kills the only daprd with a worker abruptly, with no disconnect
// callback and no unregistration, while another daprd of the app has no
// worker and, after the damping period, hosts nothing. Placement drops the
// dead daprd, the other daprd sees no daprd of the app has an executor any
// more and, once that has held for the settle period, registers the
// workflow actor types as a fallback host: status reads, starts and raises
// through it recover, and the instances run once a worker connects to it.
type hostdeath struct {
	workflow *workflow.Workflow
}

func (h *hostdeath) Setup(t *testing.T) []framework.Option {
	h.workflow = newPair(t, 2*time.Second, fallbackSettle)

	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *hostdeath) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	wf := func(ctx *task.WorkflowContext) (any, error) {
		var ev string
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(&ev); err != nil {
			return nil, err
		}
		return ev, nil
	}
	require.NoError(t, h.workflow.RegistryN(0).AddWorkflowN("wf", wf))
	require.NoError(t, h.workflow.RegistryN(1).AddWorkflowN("wf", wf))

	h.workflow.ConnectWorkerN(t, ctx, 0, h.workflow.RegistryN(0))
	h.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, h.workflow.HostsActorType(c, ctx, 1, h.workflow.WorkflowActorType(1)))
	}, time.Second*20, time.Millisecond*10)

	mgmt := h.workflow.ManagementClientN(t, ctx, 1)

	// An instance owned by the daprd with the worker, running.
	before, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("fallback-hostdeath-before"))
	require.NoError(t, err)
	_, err = mgmt.WaitForWorkflowStart(ctx, before)
	require.NoError(t, err)

	h.workflow.DaprN(0).Kill(t)

	// Every call recovers once the daprd left is a fallback host.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cctx, cancel := context.WithTimeout(ctx, time.Second*3)
		defer cancel()
		meta, merr := mgmt.FetchWorkflowMetadata(cctx, before)
		if assert.NoError(c, merr) {
			assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, meta.GetRuntimeStatus())
		}
	}, time.Second*20, time.Millisecond*10)

	const after = api.InstanceID("fallback-hostdeath-after")
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		cctx, cancel := context.WithTimeout(ctx, time.Second*3)
		defer cancel()
		_, serr := mgmt.ScheduleNewWorkflow(cctx, "wf", api.WithInstanceID(after))
		// A start whose reply an earlier attempt lost leaves the instance.
		if status.Code(serr) != codes.AlreadyExists {
			assert.NoError(c, serr)
		}
	}, time.Second*10, time.Millisecond*10)
	assert.True(t, h.workflow.HostsActorType(t, ctx, 1, h.workflow.WorkflowActorType(1)))

	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	require.NoError(t, mgmt.RaiseEvent(octx, before, "go", api.WithEventPayload("before")))
	require.NoError(t, mgmt.RaiseEvent(octx, after, "go", api.WithEventPayload("after")))

	meta, err := mgmt.FetchWorkflowMetadata(octx, after)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())

	h.workflow.ConnectWorkerN(t, ctx, 1, h.workflow.RegistryN(1))

	for id, out := range map[api.InstanceID]string{before: `"before"`, after: `"after"`} {
		meta := fworkflow.WaitForCompleted(t, ctx, mgmt, id)
		assert.Equal(t, out, meta.GetOutput().GetValue(), id)
	}
}
