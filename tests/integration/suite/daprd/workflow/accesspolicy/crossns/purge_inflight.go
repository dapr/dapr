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
	crossnswf "github.com/dapr/dapr/tests/integration/framework/process/workflow/crossns"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(purgeInflight))
}

// purgeInflight asserts the executionId-isolation guarantee under the
// terminate + purge + rerun race. A parent schedules a cross-namespace
// child that blocks mid-execution; the parent is terminated and purged
// before the child completes; a fresh parent is scheduled with the same
// instance ID (fresh executionId); the original child eventually
// finishes and its result is delivered via the xns-result reminder. The
// parent side must drop that stale result (executionId mismatch) instead
// of corrupting the new run's inbox. This covers the core cross-namespace
// safety property: cross-ns results cannot leak between runs of the
// same logical instance.
type purgeInflight struct {
	fx *crossnswf.Workflow

	childExecutions atomic.Int32
	release         chan struct{}
	firstChildGate  chan struct{}
}

func (c *purgeInflight) Setup(t *testing.T) []framework.Option {
	c.release = make(chan struct{})
	c.firstChildGate = make(chan struct{}, 1)

	c.fx = crossnswf.New(t,
		crossnswf.WithCaller("xns-purge-caller", "default"),
		crossnswf.WithTarget("xns-purge-target", "other-ns"),
		crossnswf.WithWorkflowAccessPolicyEnabled(true),
		crossnswf.WithPolicies(
			&wfaclapi.WorkflowAccessPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "xns-purge-target", Namespace: "other-ns"},
				Scoped:     common.Scoped{Scopes: []string{"xns-purge-target"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-purge-target"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-purge-caller",
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
				ObjectMeta: metav1.ObjectMeta{Name: "xns-purge-result", Namespace: "default"},
				Scoped:     common.Scoped{Scopes: []string{"xns-purge-caller"}},
				Spec: wfaclapi.WorkflowAccessPolicySpec{
					DefaultAction: wfaclapi.PolicyActionDeny,
					Rules: []wfaclapi.WorkflowAccessPolicyRule{
						{
							Callers: []wfaclapi.WorkflowCaller{{AppID: "xns-purge-caller"}},
							Operations: []wfaclapi.WorkflowOperationRule{
								{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
							},
						},
						{
							Callers: []wfaclapi.WorkflowCaller{{
								AppID:     "xns-purge-target",
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

func (c *purgeInflight) Run(t *testing.T, ctx context.Context) {
	c.fx.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()
	require.NoError(t, callerReg.AddWorkflowN("Parent", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		if err := ctx.CallChildWorkflow("Child",
			task.WithChildWorkflowAppID(c.fx.Target().AppID()),
			task.WithChildWorkflowNamespace("other-ns")).
			Await(&output); err != nil {
			return err.Error(), nil //nolint:nilerr
		}
		return output, nil
	}))

	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("Child", func(ctx *task.WorkflowContext) (any, error) {
		exec := c.childExecutions.Add(1)
		if exec == 1 {
			select {
			case c.firstChildGate <- struct{}{}:
			default:
			}
			<-c.release
			return "first-child-output", nil
		}
		return "second-child-output", nil
	}))

	callerClient := c.fx.StartListeners(t, ctx, callerReg, targetReg)

	const instanceID = "xns-purge-instance"

	id, err := callerClient.ScheduleNewWorkflow(ctx, "Parent", api.WithInstanceID(instanceID))
	require.NoError(t, err)

	select {
	case <-c.firstChildGate:
	case <-time.After(30 * time.Second):
		t.Fatal("first child did not start within timeout")
	}

	require.NoError(t, callerClient.TerminateWorkflow(ctx, id))
	require.NoError(t, callerClient.PurgeWorkflowState(ctx, id))

	_, err = callerClient.ScheduleNewWorkflow(ctx, "Parent", api.WithInstanceID(instanceID))
	require.NoError(t, err)

	close(c.release)

	assert.EventuallyWithT(t, func(ct *assert.CollectT) {
		meta, err := callerClient.FetchWorkflowMetadata(ctx, api.InstanceID(instanceID))
		if !assert.NoError(ct, err) {
			return
		}
		assert.True(ct, api.WorkflowMetadataIsComplete(meta))
		assert.Equal(ct, `"second-child-output"`, meta.GetOutput().GetValue())
	}, 60*time.Second, 250*time.Millisecond)

	assert.GreaterOrEqual(t, int(c.childExecutions.Load()), 2,
		"child must have executed for both runs (first blocked, second fresh)")
}
