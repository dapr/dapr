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
	suite.Register(new(wildcardname))
}

// wildcardname asserts that a cross-namespace rule using a glob
// (Name: "Prod*") matches workflow names by pattern. "ProdA" is allowed;
// "OtherWF" is not matched by the rule and falls through to the policy's
// default-deny. Exercises the glob matcher under the ns-aware compiler
// so that namespace keying and pattern matching compose correctly.
type wildcardname struct {
	fx *crossnswf.Workflow
}

func (c *wildcardname) Setup(t *testing.T) []framework.Option {
	c.fx = crossnswf.New(t,
		crossnswf.WithCaller("xns-wc-caller", "default"),
		crossnswf.WithTarget("xns-wc-target", "other-ns"),
		crossnswf.WithWorkflowAccessPolicyEnabled(true),
		crossnswf.WithPolicies(
			&wfaclapi.WorkflowAccessPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "xns-wc-target", Namespace: "other-ns"},
				Scoped:     common.Scoped{Scopes: []string{"xns-wc-target"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-wc-target"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-wc-caller",
								Namespace: ptr.Of("default"),
							}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Prod*", Action: wfaclapi.PolicyActionAllow},
							},
						},
					},
				},
			},
			&wfaclapi.WorkflowAccessPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "xns-wc-result", Namespace: "default"},
				Scoped:     common.Scoped{Scopes: []string{"xns-wc-caller"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-wc-caller"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-wc-target",
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

func (c *wildcardname) Run(t *testing.T, ctx context.Context) {
	c.fx.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("AllowedParent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		if err := ctx.CallChildWorkflow("ProdA",
			task.WithChildWorkflowAppID(c.fx.Target().AppID()),
			task.WithChildWorkflowNamespace("other-ns")).
			Await(&output); err != nil {
			return nil, fmt.Errorf("child failed: %w", err)
		}
		return output, nil
	}))
	require.NoError(t, callerReg.AddWorkflowN("DeniedParent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		if err := ctx.CallChildWorkflow("OtherWF",
			task.WithChildWorkflowAppID(c.fx.Target().AppID()),
			task.WithChildWorkflowNamespace("other-ns")).
			Await(&output); err != nil {
			return err.Error(), nil //nolint:nilerr
		}
		return output, nil
	}))

	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("ProdA", func(ctx *task.WorkflowContext) (any, error) {
		return "prod-a-ok", nil
	}))
	require.NoError(t, targetReg.AddWorkflowN("OtherWF", func(ctx *task.WorkflowContext) (any, error) {
		return crossnswf.ShouldNotRun, nil
	}))

	callerClient := c.fx.StartListeners(t, ctx, callerReg, targetReg)

	t.Run("workflow matching Prod* is allowed", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "AllowedParent")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.Equal(t, `"prod-a-ok"`, metadata.GetOutput().GetValue())
	})

	t.Run("workflow not matching the glob is denied by default action", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "DeniedParent")
		require.NoError(t, err)
		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.Contains(t, metadata.GetOutput().GetValue(), "WorkflowAccessPolicyDenied")
	})
}
