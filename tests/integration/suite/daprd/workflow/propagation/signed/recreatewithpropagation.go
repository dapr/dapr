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
	suite.Register(new(recreatewithpropagation))
}

// recreatewithpropagation exercises the createIfCompleted branch of
// orchestrator.createWorkflowInstance. The fresh-create branch (state ==
// nil) is covered by signed/multiapp; this test forces the second branch
// by re-using the same child instance ID after the prior run completed.
//
// Flow:
//  1. Parent on App0 calls child on App1 with a fixed instance ID and
//     PropagateLineage. Child completes.
//  2. A second parent invocation calls child on App1 with the SAME
//     instance ID and PropagateLineage. The orchestrator finds existing
//     completed state and routes through createIfCompleted, which must
//     run the propagated-history verify+absorb path before resetting
//     state.
//  3. Both child runs must complete successfully; App1 should hold App0's
//     foreign signing cert in ext-sigcert after the recreate.
type recreatewithpropagation struct {
	workflow *procworkflow.Workflow

	childRuns atomic.Int32
}

func (s *recreatewithpropagation) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *recreatewithpropagation) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const fixedChildID = "recreate-with-prop-child"

	app0Reg := s.workflow.Registry()
	app1Reg := s.workflow.RegistryN(1)

	app1Reg.AddWorkflowN("recreateChild", func(ctx *task.WorkflowContext) (any, error) {
		s.childRuns.Add(1)
		return "child-done", nil
	})

	app0Reg.AddWorkflowN("recreateParent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		return out, ctx.CallChildWorkflow("recreateChild",
			task.WithChildWorkflowInstanceID(fixedChildID),
			task.WithChildWorkflowAppID(s.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out)
	})

	client0 := s.workflow.BackendClient(t, ctx)
	s.workflow.BackendClientN(t, ctx, 1)

	// First invocation - fresh create branch.
	parent1, err := client0.ScheduleNewWorkflow(ctx, "recreateParent")
	require.NoError(t, err)
	meta, err := client0.WaitForWorkflowCompletion(ctx, parent1, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))

	// Second invocation under a NEW parent ID but the SAME child ID.
	// The child's existing state is completed, so create.go routes
	// through createIfCompleted which must run propagated-history
	// verify+absorb on the new payload before state.Reset.
	parent2, err := client0.ScheduleNewWorkflow(ctx, "recreateParent")
	require.NoError(t, err)
	meta, err = client0.WaitForWorkflowCompletion(ctx, parent2, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta),
		"second parent must complete; createIfCompleted branch with signed propagation must accept valid payload")

	assert.Equal(t, int32(2), s.childRuns.Load(),
		"child should have run twice (once per parent)")

	// The child's ext-sigcert must hold App0's signing cert; the
	// createIfCompleted branch is responsible for re-running absorption
	// after Reset, otherwise downstream lookups would miss the cert.
	extCerts := fworkflow.ReadExtSigCerts(t, ctx, s.workflow.DB(), fixedChildID)
	assert.NotEmpty(t, extCerts,
		"child ext-sigcert should contain App0's foreign cert after createIfCompleted recreate")
}
