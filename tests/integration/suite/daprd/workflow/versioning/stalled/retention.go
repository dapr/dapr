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

package stalled

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(retention))
}

type retention struct {
	workflow *workflow.Workflow
}

func (r *retention) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: wfpolicy
spec:
  workflow:
    stateRetentionPolicy:
      completed: "1s"
`)),
	)
	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *retention) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var runv1 atomic.Bool
	var runv2 atomic.Bool

	wf1 := func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		runv1.Store(true)
		return nil, nil
	}

	r.workflow.Registry().AddVersionedOrchestratorN("workflow", "v1", true, wf1)

	clientCtx, cancelClient := context.WithCancel(ctx)
	defer cancelClient()
	client := r.workflow.BackendClient(t, clientCtx)
	id, err := client.ScheduleNewOrchestration(ctx, "workflow")
	require.NoError(t, err)

	wf.WaitForOrchestratorStartedEvent(t, ctx, client, id)

	cancelClient()
	r.workflow.ResetRegistry(t)

	r.workflow.Registry().AddVersionedOrchestratorN("workflow", "v2", true, func(ctx *task.OrchestrationContext) (any, error) {
		if err = ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
			return nil, err
		}
		runv2.Store(true)
		return nil, nil
	})

	clientCtx, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	client = r.workflow.BackendClient(t, clientCtx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		require.NoError(c, client.RaiseEvent(ctx, id, "Continue"))
	}, 10*time.Second, 10*time.Millisecond)

	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	cancelClient()
	r.workflow.ResetRegistry(t)
	r.workflow.Registry().AddVersionedOrchestratorN("workflow", "v1", true, wf1)

	clientCtx, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	client = r.workflow.BackendClient(t, clientCtx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		ids, err := client.ListInstanceIDs(ctx)
		require.NoError(c, err)
		assert.Empty(c, ids.GetInstanceIds())
	}, time.Second*10, 10*time.Millisecond)
}
