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

package workflow

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(agentloop))
}

type agentloop struct {
	workflow *workflow.Workflow
}

func (a *agentloop) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *agentloop) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	const (
		iterations  = 8
		instances   = 3
		eventName   = "agent-event"
		waitTimeout = 30 * time.Second
	)

	var raiseClient atomic.Pointer[client.TaskHubGrpcClient]

	r := a.workflow.Registry()

	require.NoError(t, r.AddActivityN("step", func(actx task.ActivityContext) (any, error) {
		var in struct {
			Instance string `json:"instance"`
			Iter     int    `json:"iter"`
		}
		if err := actx.GetInput(&in); err != nil {
			return nil, err
		}
		time.Sleep(time.Duration(100+(in.Iter%4)*100) * time.Millisecond)
		if err := raiseClient.Load().RaiseEvent(actx.Context(), api.InstanceID(in.Instance), eventName); err != nil {
			return nil, err
		}
		return in.Iter, nil
	}))

	require.NoError(t, r.AddWorkflowN("agent", func(octx *task.WorkflowContext) (any, error) {
		total := 0
		for i := range iterations {
			if err := octx.WaitForSingleEvent(eventName, 15*time.Second).Await(nil); err != nil {
				return nil, err
			}
			var out int
			if err := octx.CallActivity("step", task.WithActivityInput(struct {
				Instance string `json:"instance"`
				Iter     int    `json:"iter"`
			}{Instance: string(octx.ID), Iter: i})).Await(&out); err != nil {
				return nil, err
			}
			total += out
		}
		return total, nil
	}))

	cl := a.workflow.BackendClient(t, ctx)
	raiseClient.Store(cl)

	for inst := range instances {
		id, err := cl.ScheduleNewWorkflow(ctx, "agent",
			api.WithInstanceID(api.InstanceID(fmt.Sprintf("agentloop-%d", inst))),
		)
		require.NoError(t, err)

		require.NoError(t, cl.RaiseEvent(ctx, id, eventName))

		waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
		meta, err := cl.WaitForWorkflowCompletion(waitCtx, id)
		cancel()
		require.NoErrorf(t, err, "instance %d: agent loop hung (event or activity completion lost)", inst)
		require.Equalf(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String(),
			"instance %d: %v", inst, meta.GetFailureDetails())
	}
}
