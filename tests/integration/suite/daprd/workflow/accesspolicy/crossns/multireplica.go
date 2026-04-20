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

package crossns

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	crossnswf "github.com/dapr/dapr/tests/integration/framework/process/workflow/crossns"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(multireplica))
}

// multireplica asserts cross-namespace invocation works when both sides
// run multiple daprd replicas for the same (appID, namespace) pair. It
// exercises two independent routing surfaces that did not exist in a
// single-replica test:
//
//  1. Name resolution. Each xns-dispatch hop resolves the target app to
//     a set of sidecar addresses; which replica the hop lands on is
//     non-deterministic, so the RPC path must tolerate any replica being
//     the entry point.
//  2. Actor placement. The xns-exec reminder fires on whichever replica
//     currently owns the workflow/activity actor — not necessarily the
//     replica that accepted the RPC. Placement routes the reminder
//     delivery, and the parent-side xns-result reminder is routed the
//     same way on its return trip.
//
// Multiple parents scheduled in parallel give placement the opportunity
// to distribute child actors across both target replicas, and similarly
// distribute result-callback landings across both caller replicas.
type multireplica struct {
	fx *crossnswf.Workflow

	childRuns atomic.Int32
}

func (c *multireplica) Setup(t *testing.T) []framework.Option {
	c.fx = crossnswf.New(t,
		crossnswf.WithCaller("xns-mr-caller", "default"),
		crossnswf.WithTarget("xns-mr-target", "other-ns"),
		crossnswf.WithCallerReplicas(2),
		crossnswf.WithTargetReplicas(2),
		crossnswf.WithWorkflowAccessPolicyEnabled(true),
		crossnswf.WithPolicies(
			&wfaclapi.WorkflowAccessPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "xns-mr-target", Namespace: "other-ns"},
				Scoped:     common.Scoped{Scopes: []string{"xns-mr-target"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-mr-target"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-mr-caller",
								Namespace: ptr.Of("default"),
							}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
					},
				},
			},
			&wfaclapi.WorkflowAccessPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "xns-mr-result", Namespace: "default"},
				Scoped:     common.Scoped{Scopes: []string{"xns-mr-caller"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-mr-caller"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-mr-target",
								Namespace: ptr.Of("other-ns"),
							}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
					},
				},
			},
		),
	)
	return []framework.Option{framework.WithProcesses(c.fx)}
}

func (c *multireplica) Run(t *testing.T, ctx context.Context) {
	c.fx.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		if err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(c.fx.Target().AppID()),
			task.WithChildWorkflowNamespace("other-ns")).
			Await(&output); err != nil {
			return nil, fmt.Errorf("child failed: %w", err)
		}
		return output, nil
	}))

	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		run := c.childRuns.Add(1)
		return fmt.Sprintf("child-%d", run), nil
	}))

	callerClient := c.fx.StartListeners(t, ctx, callerReg, targetReg)

	const parallel = 8
	ids := make([]api.InstanceID, parallel)
	for i := range ids {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "Parent",
			api.WithInstanceID(api.InstanceID(fmt.Sprintf("mr-%d", i))))
		require.NoError(t, err)
		ids[i] = id
	}

	for _, id := range ids {
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata), "parent %s not complete", id)
		assert.Contains(t, metadata.GetOutput().GetValue(), "child-")
	}

	assert.EqualValues(t, parallel, c.childRuns.Load(),
		"each parent must have triggered exactly one child execution")
}
