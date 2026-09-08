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

package signing

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(childidcollide))
}

// childidcollide verifies that, under history signing, the failure the
// runtime synthesizes when a ContinueAsNew generation re-creates a child
// under an instance ID still held by the previous generation is delivered
// to the parent as an ordinary child failure. A synthesized failure carries
// no attestation by design, so it must be recognised as locally authored
// rather than rejected as tampering (which would tombstone the parent).
type childidcollide struct {
	workflow *workflow.Workflow
}

func (c *childidcollide) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t, workflow.WithHistorySigning(t))

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *childidcollide) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const parentID = "signing-childid-collide"
	const pinnedID = parentID + "-pinned"

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseCh:
		default:
			close(releaseCh)
		}
	})

	reg := c.workflow.Registry()
	reg.AddWorkflowN("blocker", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("block").Await(nil)
	})
	reg.AddActivityN("block", func(actx task.ActivityContext) (any, error) {
		inActivity.Store(true)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseCh:
			return nil, nil
		}
	})
	reg.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		if gen == 0 {
			ctx.CallChildWorkflow("blocker", task.WithChildWorkflowInstanceID(pinnedID))
			ctx.WaitForSingleEvent("proceed", time.Minute).Await(nil)
			ctx.ContinueAsNew(1)
			return nil, nil
		}
		// Surface the collision as output so a tombstoned (FAILED) parent
		// is distinguishable from one that observed the child failure.
		if err := ctx.CallChildWorkflow("blocker",
			task.WithChildWorkflowInstanceID(pinnedID),
		).Await(nil); err != nil {
			return err.Error(), nil //nolint:nilerr
		}
		return "no error", nil
	})

	client := c.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "parent",
		api.WithInstanceID(parentID),
		api.WithInput(0),
	)
	require.NoError(t, err)

	require.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)
	require.NoError(t, client.RaiseEvent(ctx, parentID, "proceed"))

	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err := client.WaitForWorkflowCompletion(waitCtx, parentID)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String(),
		"parent was tombstoned instead of observing the child failure; failure details: %v", meta.GetFailureDetails())
	assert.Contains(t, meta.GetOutput().GetValue(), "already exists")

	ometa, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(pinnedID))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING.String(), ometa.GetRuntimeStatus().String())

	close(releaseCh)
	ometa, err = client.WaitForWorkflowCompletion(ctx, pinnedID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), ometa.GetRuntimeStatus().String())
}
