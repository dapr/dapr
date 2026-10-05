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

package loadbalance

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(haltparked))
}

// haltparked parks activity completions on executor actors, then moves their
// keys to another daprd. Every completion wait uses the watch-stream fallback,
// and each activity watch stream opens 5s late, so the completion that the
// worker sends parks on the executor actor first. Then a worker connects to
// the second daprd, and placement moves about half of the keys to it. When a
// rebalance halts an executor actor, the actor must hand its parked
// completion to the key's new owner, where the late watch stream attaches.
type haltparked struct {
	workflow *workflow.Workflow
}

func (h *haltparked) Setup(t *testing.T) []framework.Option {
	h.workflow = workflow.NewClustered(t, 2, daprd.WithExecOptions(exec.WithEnvVars(t,
		"DAPR_WORKFLOW_TEST_FORCE_WATCH_FALLBACK", "1000000",
		"DAPR_WORKFLOW_TEST_ACTIVITY_WATCH_DELAY", "5s",
	)))

	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *haltparked) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	const instances = 12

	var ran atomic.Int32
	for i := range 2 {
		reg := h.workflow.RegistryN(i)
		require.NoError(t, reg.AddWorkflowN("haltparked", func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity("act").Await(nil)
		}))
		require.NoError(t, reg.AddActivityN("act", func(task.ActivityContext) (any, error) {
			ran.Add(1)
			return nil, nil
		}))
	}

	// Only daprd 0 has a worker, so it hosts every workflow, activity and
	// executor actor.
	cl := h.workflow.BackendClientN(t, ctx, 0)

	ids := make([]api.InstanceID, instances)
	for i := range ids {
		ids[i] = api.InstanceID(fmt.Sprintf("haltparked-%d", i))
		_, err := cl.ScheduleNewWorkflow(ctx, "haltparked", api.WithInstanceID(ids[i]))
		require.NoError(t, err)
	}

	// Every activity ran, and its completion is parked on the executor actor
	// because its watch stream is not open yet.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, ran.Load(), int32(instances))
	}, time.Second*10, time.Millisecond*10)

	// The worker on daprd 1 adds daprd 1 to the placement ring of every
	// workflow actor type, and daprd 0 halts the actors of the keys that move.
	h.workflow.BackendClientN(t, ctx, 1)

	wctx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()
	fworkflow.WaitForAllCompleted(t, wctx, cl, ids...)
}
