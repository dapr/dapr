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

package workflow

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(replicas))
}

// replicas checks ten workflows finish when their workflow turns are pulled across two
// replicas with one workflow slot each.
type replicas struct {
	workflow *workflow.Workflow
}

func (r *replicas) Setup(t *testing.T) []framework.Option {
	const appID = "workflow-replicas"
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowreplicas
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 2
    dispatchMode: pull
`
	r.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *replicas) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var ran [2]atomic.Int64
	for i := range 2 {
		require.NoError(t, r.workflow.RegistryN(i).AddWorkflowN("steps", func(ctx *task.WorkflowContext) (any, error) {
			for range 3 {
				if err := ctx.CallActivity("step").Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}))
		require.NoError(t, r.workflow.RegistryN(i).AddActivityN("step", func(task.ActivityContext) (any, error) {
			ran[i].Add(1)
			return nil, nil
		}))
	}

	client := r.workflow.BackendClientN(t, ctx, 0)
	r.workflow.BackendClientN(t, ctx, 1)

	ids := make([]api.InstanceID, 10)
	for i := range ids {
		id, err := client.ScheduleNewWorkflow(ctx, "steps")
		require.NoError(t, err)
		ids[i] = id
	}

	for _, id := range ids {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
	assert.Equal(t, int64(30), ran[0].Load()+ran[1].Load())
}
