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

package orderingkey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/framework/workflow/runs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(daprapi))
}

// daprapi checks the ordering options of the Dapr StartWorkflow API, and that
// both APIs reject invalid ones.
type daprapi struct {
	workflow *workflow.Workflow
}

func (d *daprapi) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *daprapi) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)
	var r runs.Recorder
	d.workflow.Registry().AddWorkflowN("wait", r.Waiter)
	cl := d.workflow.BackendClient(t, ctx)
	gcl := d.workflow.GRPCClient(t, ctx)

	startAPI := func(id string, options map[string]string) error {
		_, err := gcl.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "wait",
			InstanceId:        id,
			Options:           options,
		})
		return err
	}
	const keyOpt, policyOpt = "dapr.workflow.ordering_key", "dapr.workflow.ordering_on_predecessor_failure"
	keyed := map[string]string{keyOpt: "api"}
	cancel := map[string]string{keyOpt: "api", policyOpt: "cancel"}
	require.NoError(t, startAPI("a", keyed))
	require.NoError(t, startAPI("b", keyed))
	require.NoError(t, startAPI("c", cancel))
	r.AssertStarted(t, "a")
	r.AssertQueued(t, "b", "c")
	r.Finish(t, ctx, cl, "a")
	r.AssertStarted(t, "b")
	r.Fail(t, ctx, cl, "b")
	wf.WaitForRuntimeStatus(t, ctx, cl, "c", protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED)
	assert.NotContains(t, r.Started(), "c", "the cancel policy applies")

	for _, opts := range []map[string]string{
		{keyOpt: "a.b"},
		{keyOpt: "api", policyOpt: "nope"},
		{policyOpt: "block"},
	} {
		require.ErrorContains(t, startAPI("bad", opts), "ordering", opts)
	}
	_, err := cl.ScheduleNewWorkflow(ctx, "wait", api.WithInstanceID("bad"), api.WithOrdering(api.Ordering{Key: "api", OnPredecessorFailure: 7}))
	require.ErrorContains(t, err, "invalid workflow ordering failure policy")
}
