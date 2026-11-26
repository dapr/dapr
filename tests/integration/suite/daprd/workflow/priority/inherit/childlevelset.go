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

package inherit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(childlevelset))
}

// childlevelset checks a child whose config entry sets only a level still
// inherits its parent's weight, not the default's.
type childlevelset struct {
	workflow *workflow.Workflow
}

func (c *childlevelset) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithFastPath(false),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: priority
spec:
  workflow:
    maxConcurrentActivityInvocations: 10
    activityDispatchMode: pull
    priority:
      default:
        priorityLevel: high
        fairnessWeight: 2
      workflows:
        - name: parent
          priorityLevel: critical
          fairnessWeight: 6
        - name: child
          priorityLevel: low
`)),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *childlevelset) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	release := make(chan struct{})
	registry := c.workflow.Registry()
	require.NoError(t, registry.AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		select {
		case <-release:
		case <-ctx.Context().Done():
		}
		return nil, nil
	}))
	require.NoError(t, registry.AddWorkflowN("parent", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallChildWorkflow("child").Await(nil)
	}))
	require.NoError(t, registry.AddWorkflowN("child", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("act").Await(nil)
	}))

	client := c.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	sched := c.workflow.SchedulerClientN(t, ctx, 0)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		jobs, listErr := c.workflow.ListActivityReminders(ctx, sched, 0)
		if assert.NoError(col, listErr) && assert.Len(col, jobs, 1) {
			meta := jobs[0].GetMetadata()
			// child's config entry sets low but no weight, so parent's weight 6 is inherited instead of the default's 2.
			assert.Equal(col, schedulerv1.PriorityLevel_PRIORITY_LEVEL_LOW, meta.GetPriority().GetLevel())
			assert.Equal(col, uint32(6), meta.GetPriority().GetFairnessWeight())
		}
	}, time.Second*10, time.Millisecond*10)

	close(release)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
}
