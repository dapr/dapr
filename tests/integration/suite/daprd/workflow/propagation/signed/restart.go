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

package signed

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(restart))
}

// restart is the positive control for the tamper tests: restart the receiver
// daprd while a child workflow is mid-flight (after receiving signed
// propagation but before completion), and confirm the load-time
// re-verification accepts the untampered persisted state. The child resumes
// and completes normally. This guards against false-positives in the
// cold-start re-verification path — clean state must round-trip through
// receive -> persist -> load -> re-verify without rejection.
type restart struct {
	workflow *procworkflow.Workflow

	childInstanceID atomic.Value // string
	childResumed    atomic.Bool
}

func (s *restart) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *restart) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	app0Reg := s.workflow.Registry()
	app1Reg := s.workflow.RegistryN(1)

	app1Reg.AddWorkflowN("restartChild", func(ctx *task.WorkflowContext) (any, error) {
		s.childInstanceID.Store(string(ctx.ID))
		var p string
		if err := ctx.WaitForSingleEvent("continue", 30*time.Second).Await(&p); err != nil {
			return nil, err
		}
		s.childResumed.Store(true)
		return p, nil
	})

	app0Reg.AddWorkflowN("restartParent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		return out, ctx.CallChildWorkflow("restartChild",
			task.WithChildWorkflowAppID(s.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out)
	})

	client0 := s.workflow.BackendClient(t, ctx)
	s.workflow.BackendClientN(t, ctx, 1)

	parentID, err := client0.ScheduleNewWorkflow(ctx, "restartParent")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		v := s.childInstanceID.Load()
		assert.NotNil(c, v)
		if v != nil {
			assert.NotEmpty(c, v.(string))
		}
	}, 20*time.Second, 10*time.Millisecond)
	childID := s.childInstanceID.Load().(string)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountPropagatedHistoryRows(t, ctx, s.workflow.DB(), childID))
	}, 20*time.Second, 10*time.Millisecond)

	// Restart App1's daprd: orchestrator actor reloads from store and
	// re-runs propagated-history verification. Untampered state must
	// pass.
	s.workflow.DaprN(1).Restart(t, ctx)
	s.workflow.DaprN(1).WaitUntilRunning(t, ctx)
	client1 := s.workflow.BackendClientN(t, ctx, 1)

	require.NoError(t, client1.RaiseEvent(ctx, api.InstanceID(childID), "continue", api.WithEventPayload("done")))

	// Both child and parent should complete cleanly.
	cmeta, err := client1.WaitForWorkflowCompletion(ctx, api.InstanceID(childID), api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, cmeta.GetRuntimeStatus(),
		"child must complete normally after a clean restart; load-time re-verify must not reject untampered state")
	assert.True(t, s.childResumed.Load(),
		"child should observe the awaited event after restart")

	pmeta, err := client0.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, pmeta.GetRuntimeStatus(),
		"parent must complete once the child finishes")
}
