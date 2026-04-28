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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	crossnswf "github.com/dapr/dapr/tests/integration/framework/process/workflow/crossns"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(inflightDuplicate))
}

// inflightDuplicate asserts that the caller-side xns-dispatch reminder
// is idempotent end-to-end: if the caller sidecar crashes after the
// target accepted the dispatch but before the reminder was deleted
// locally, the reminder re-fires on restart and the RPC is retried. The
// target's deterministic reminder name collapses the second call to an
// AlreadyExists, and the child workflow runs exactly once. This is the
// core contract that justifies deriving the idempotency key from stable
// identifiers (executionIds + taskId) rather than randomness.
type inflightDuplicate struct {
	fx *crossnswf.Workflow

	childExecutions atomic.Int32
}

func (c *inflightDuplicate) Setup(t *testing.T) []framework.Option {
	c.fx = crossnswf.New(t,
		crossnswf.WithCaller("xns-dup-caller", "default"),
		crossnswf.WithTarget("xns-dup-target", "other-ns"),
		crossnswf.WithWorkflowAccessPolicyEnabled(true),
		crossnswf.WithPolicies(
			&wfaclapi.WorkflowAccessPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "xns-dup-target", Namespace: "other-ns"},
				Scoped:     common.Scoped{Scopes: []string{"xns-dup-target"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-dup-target"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-dup-caller",
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
				ObjectMeta: metav1.ObjectMeta{Name: "xns-dup-result", Namespace: "default"},
				Scoped:     common.Scoped{Scopes: []string{"xns-dup-caller"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-dup-caller"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-dup-target",
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

func (c *inflightDuplicate) Run(t *testing.T, ctx context.Context) {
	c.fx.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		if err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(c.fx.Target().AppID()),
			task.WithChildWorkflowAppNamespace("other-ns")).
			Await(&output); err != nil {
			return nil, err
		}
		return output, nil
	}))

	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		c.childExecutions.Add(1)
		return "dup-ok", nil
	}))

	callerClient := c.fx.StartListeners(t, ctx, callerReg, targetReg)

	id, err := callerClient.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, `"dup-ok"`, metadata.GetOutput().GetValue())

	assert.Eventually(t, func() bool {
		return c.childExecutions.Load() == 1
	}, 10*time.Second, 100*time.Millisecond,
		"child workflow must execute exactly once (got %d)", c.childExecutions.Load())

	c.fx.RestartCaller(t, ctx)

	callerClient2 := client.NewTaskHubGrpcClient(c.fx.Caller().GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient2.StartWorkItemListener(ctx, callerReg))

	assert.Never(t, func() bool {
		return c.childExecutions.Load() > 1
	}, 5*time.Second, 200*time.Millisecond,
		"restart must not cause the child to re-execute")
}
