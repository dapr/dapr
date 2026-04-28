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
	"errors"
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
	suite.Register(new(childfails))
}

// childfails asserts that a BUSINESS failure inside the child workflow
// (policy-allowed, target executes, returns an error) is faithfully
// relayed back to the parent through the xns-result bridge. This
// distinguishes a policy-denied failure (which does not reach the
// target) from a target-returned failure (which does) — both paths must
// surface in the parent but carry different semantics.
type childfails struct {
	fx *crossnswf.Workflow
}

func (c *childfails) Setup(t *testing.T) []framework.Option {
	c.fx = crossnswf.New(t,
		crossnswf.WithCaller("xns-fail-caller", "default"),
		crossnswf.WithTarget("xns-fail-target", "other-ns"),
		crossnswf.WithWorkflowAccessPolicyEnabled(true),
		crossnswf.WithPolicies(
			&wfaclapi.WorkflowAccessPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "xns-fail-target", Namespace: "other-ns"},
				Scoped:     common.Scoped{Scopes: []string{"xns-fail-target"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-fail-target"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-fail-caller",
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
				ObjectMeta: metav1.ObjectMeta{Name: "xns-fail-result", Namespace: "default"},
				Scoped:     common.Scoped{Scopes: []string{"xns-fail-caller"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-fail-caller"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-fail-target",
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

func (c *childfails) Run(t *testing.T, ctx context.Context) {
	c.fx.WaitUntilRunning(t, ctx)

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
		return nil, errors.New("child-business-error")
	}))

	callerClient := c.fx.StartListeners(t, ctx, callerReg, targetReg)

	id, err := callerClient.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Contains(t, metadata.GetOutput().GetValue(), "child-business-error")
	assert.NotContains(t, metadata.GetOutput().GetValue(), "WorkflowAccessPolicyDenied")
}
