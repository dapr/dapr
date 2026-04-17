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
)

func init() {
	suite.Register(new(samensonly))
}

// samensonly asserts that a policy rule which does not set a caller namespace
// (nil Namespace pointer -> resolves to the target's own namespace) MUST NOT
// match a caller with the same appID in a different namespace. Two apps with
// appID "my-app" in namespaces A and B are different principals, and the
// ns-aware evaluator must treat them so. This is the ingress-side VULN
// prevention for cross-namespace confusion: the target's legacy same-ns rule
// cannot be exploited by a cross-ns caller that happens to share the appID.
type samensonly struct {
	fx *crossnswf.Workflow
}

func (c *samensonly) Setup(t *testing.T) []framework.Option {
	c.fx = crossnswf.New(t,
		crossnswf.WithCaller("dup-app", "default"),
		crossnswf.WithTarget("xns-samens-target", "other-ns"),
		crossnswf.WithWorkflowAccessPolicyEnabled(true),
		crossnswf.WithPolicies(&wfaclapi.WorkflowAccessPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "xns-samens", Namespace: "other-ns"},
			Scoped:     common.Scoped{Scopes: []string{"xns-samens-target"}},
			Spec: wfaclapi.WorkflowAccessPolicySpec{
				DefaultAction: wfaclapi.PolicyActionDeny,
				Rules: []wfaclapi.WorkflowAccessPolicyRule{
					{
						Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-samens-target"}},
						Operations: []wfaclapi.WorkflowOperationRule{
							{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
						},
					},
					{
						Callers: []wfaclapi.WorkflowCaller{{AppID: "dup-app"}},
						Operations: []wfaclapi.WorkflowOperationRule{
							{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Child", Action: wfaclapi.PolicyActionAllow},
						},
					},
				},
			},
		}),
	)
	return []framework.Option{framework.WithProcesses(c.fx)}
}

func (c *samensonly) Run(t *testing.T, ctx context.Context) {
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
		return crossnswf.ShouldNotRun, nil
	}))

	callerClient := c.fx.StartListeners(t, ctx, callerReg, targetReg)

	id, err := callerClient.ScheduleNewWorkflow(ctx, "Parent")
	require.NoError(t, err)

	metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metadata))
	assert.Contains(t, metadata.GetOutput().GetValue(), "WorkflowAccessPolicyDenied")
}
