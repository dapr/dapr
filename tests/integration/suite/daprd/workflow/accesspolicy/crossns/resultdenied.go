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
	"testing"
	"time"

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
	suite.Register(new(resultdenied))
}

// resultdenied asserts that cross-namespace result delivery is
// policy-gated on the PARENT side independently of the target-side
// dispatch gate. The target's policy allows the caller to schedule the
// child, so the child runs to completion; but the caller's policy does
// NOT list the target app as an allowed caller for result delivery, so
// DeliverWorkflowResultCrossNamespace on the parent returns
// PermissionDenied and the child's result never lands in the parent's
// inbox. The parent remains in RUNNING — it never receives the
// completion event. Exercises the IsCallerKnown ns-aware check on the
// result hop.
type resultdenied struct {
	fx *crossnswf.Workflow
}

func (c *resultdenied) Setup(t *testing.T) []framework.Option {
	c.fx = crossnswf.New(t,
		crossnswf.WithCaller("xns-rd-caller", "default"),
		crossnswf.WithTarget("xns-rd-target", "other-ns"),
		crossnswf.WithWorkflowAccessPolicyEnabled(true),
		crossnswf.WithPolicies(
			&wfaclapi.WorkflowAccessPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "xns-rd-target", Namespace: "other-ns"},
				Scoped:     common.Scoped{Scopes: []string{"xns-rd-target"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-rd-target"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-rd-caller",
								Namespace: ptr.Of("default"),
							}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Child", Action: wfaclapi.PolicyActionAllow},
							},
						},
					},
				},
			},
			&wfaclapi.WorkflowAccessPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "xns-rd-caller", Namespace: "default"},
				Scoped:     common.Scoped{Scopes: []string{"xns-rd-caller"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-rd-caller"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "unrelated-app",
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

func (c *resultdenied) Run(t *testing.T, ctx context.Context) {
	c.fx.WaitUntilRunning(t, ctx)

	childRan := make(chan struct{}, 1)

	callerReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		if err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(c.fx.Target().AppID()),
			task.WithChildWorkflowAppNamespace("other-ns")).
			Await(&output); err != nil {
			return err.Error(), nil //nolint:nilerr
		}
		return output, nil
	}))

	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		select {
		case childRan <- struct{}{}:
		default:
		}
		return "child-ran", nil
	}))

	callerClient := c.fx.StartListeners(t, ctx, callerReg, targetReg)

	id, err := callerClient.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	select {
	case <-childRan:
	case <-time.After(30 * time.Second):
		t.Fatal("child workflow did not execute on target within timeout")
	}

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = callerClient.WaitForWorkflowCompletion(waitCtx, id, api.WithFetchPayloads(true))
	assert.Error(t, err, "parent must not complete when result delivery is denied by policy")
}
